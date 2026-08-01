package analyzer

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// ValidateFragments checks fragment declarations within one file: names
// must be unique in their scope (FR-030, GHTMX-E0301). Package-wide
// uniqueness — required for the generated entry points — is enforced when
// fragment code generation runs over the whole package.
func ValidateFragments(file *parser.TemplateFile, sink *diag.Sink) {
	seen := map[string]diag.Position{}
	record := func(f *parser.FragmentDeclaration) {
		pos := diag.Position{
			File:  file.Filepath,
			Line:  f.Range.From.Line + 1,
			Col:   f.Range.From.Col + 1,
			Index: f.Range.From.Index,
		}
		if first, dup := seen[f.Name]; dup {
			sink.Add(diag.DuplicateFragment, pos,
				fmt.Sprintf("duplicate fragment %q: first declared at %s", f.Name, first),
				"rename one of the fragments; fragment names are unique within their scope")
			return
		}
		seen[f.Name] = pos
	}

	// Fragments take no children: a { children... } expression inside a
	// fragment body has no caller to supply it (GHTMX-E0302). The walk
	// prunes nested fragment declarations — each fragment reports its own
	// body on its own visit.
	checkChildren := func(f *parser.FragmentDeclaration) {
		var walk func(nodes []parser.Node)
		walk = func(nodes []parser.Node) {
			for _, n := range nodes {
				if _, isFrag := n.(*parser.FragmentDeclaration); isFrag {
					continue
				}
				if c, isChildren := n.(*parser.ChildrenExpression); isChildren {
					sink.Add(diag.FragmentChildren, diag.Position{
						File:  file.Filepath,
						Line:  c.Range.From.Line + 1,
						Col:   c.Range.From.Col + 1,
						Index: c.Range.From.Index,
					},
						fmt.Sprintf("fragment %q cannot use { children... }: a standalone fragment render has no caller to supply children", f.Name),
						"pass the content as a typed parameter instead")
				}
				if c, isComposite := n.(parser.CompositeNode); isComposite {
					walk(c.ChildNodes())
				}
			}
		}
		walk(f.Children)
	}

	visit := func(n parser.Node) {
		if f, ok := n.(*parser.FragmentDeclaration); ok {
			record(f)
			checkChildren(f)
		}
	}
	for _, node := range file.Nodes {
		switch t := node.(type) {
		case *parser.FragmentDeclaration:
			record(t)
			checkChildren(t)
			walkNodes(t.Children, visit)
		case *parser.HTMLTemplate:
			walkNodes(t.Children, visit)
		}
	}
}

// FragmentInfo describes one declared fragment in the compiled set.
type FragmentInfo struct {
	Name       string
	PkgPath    string
	ParamCount int
	Variadic   bool
	Pos        diag.Position
	TopLevel   bool
}

// Exported reports whether the fragment is visible outside its package.
func (f FragmentInfo) Exported() bool {
	r, _ := utf8.DecodeRuneInString(f.Name)
	return unicode.IsUpper(r)
}

// fragmentRef is a candidate fragment reference: any @Name(args) /
// @pkg.Name(args) templ element. Whether it actually targets a fragment is
// decided at Check time against the whole-set registry; references to
// ordinary components are ignored.
type fragmentRef struct {
	qualifier   string // package alias, "" for same-package references
	name        string
	argCount    int  // -1 when the reference is not a call
	spread      bool // the call ends in a ... spread argument
	hasChildren bool // the reference carries a { ... } children block
	shadowed    bool // the name is a parameter of the enclosing declaration
	filePkg     string
	imports     map[string]string
	pos         diag.Position
}

// CollectFragments records the file's fragment declarations and its
// candidate fragment references for whole-set resolution (FR-032).
// pkgPath is the import path of the package containing the file.
func (s *SetAnalysis) CollectFragments(file *parser.TemplateFile, pkgPath string) {
	frags := computeFragmentFacts(file, pkgPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fragments == nil {
		s.fragments = map[string]*fileFragments{}
	}
	s.fragments[file.Filepath] = frags
}

func computeFragmentFacts(file *parser.TemplateFile, pkgPath string) *fileFragments {
	var decls []FragmentInfo
	var refs []fragmentRef
	imports := templateImports(file)

	pos := func(rng parser.Range) diag.Position {
		return diag.Position{
			File:  file.Filepath,
			Line:  rng.From.Line + 1,
			Col:   rng.From.Col + 1,
			Index: rng.From.Index,
		}
	}
	var nodes []*declNode
	record := func(f *parser.FragmentDeclaration) *declNode {
		count, variadic := countParams(f.Expression.Value)
		decls = append(decls, FragmentInfo{
			Name:       f.Name,
			PkgPath:    pkgPath,
			ParamCount: count,
			Variadic:   variadic,
			Pos:        pos(f.Range),
			TopLevel:   f.TopLevel,
		})
		n := &declNode{
			name:    f.Name,
			kind:    "fragment",
			pkgPath: pkgPath,
			pos:     pos(f.Range),
			params:  signatureParams(f.Expression.Value),
		}
		nodes = append(nodes, n)
		return n
	}
	// visitRef records one candidate reference expression under the
	// enclosing declaration and, when it is an unshadowed call, adds the
	// graph edge (FR-053). Bare references are component values (a
	// declared template or fragment must be called to render), and a name
	// shadowed by an enclosing parameter refers to that parameter — in
	// both cases there is no edge and no resolution check.
	visitRef := func(exprValue string, hasChildren bool, rng parser.Range, curr *declNode) {
		ref, ok := parseFragmentRef(exprValue)
		if !ok {
			return
		}
		ref.hasChildren = hasChildren
		ref.shadowed = ref.qualifier == "" && curr.params[ref.name]
		ref.filePkg = pkgPath
		ref.imports = imports
		ref.pos = pos(rng)
		refs = append(refs, ref)
		if ref.shadowed || ref.argCount == -1 {
			return
		}
		targetPkg := pkgPath
		if ref.qualifier != "" {
			targetPkg = imports[ref.qualifier] // "" drops the edge below
		}
		if targetPkg != "" {
			curr.edges = append(curr.edges, edgeRef{targetPkg: targetPkg, name: ref.name, pos: ref.pos})
		}
	}
	// walkScoped tracks the enclosing declaration so every reference and
	// nested declaration site becomes a graph edge from it (FR-053).
	var walkScoped func(ns []parser.Node, curr *declNode)
	walkScoped = func(ns []parser.Node, curr *declNode) {
		for _, n := range ns {
			switch t := n.(type) {
			case *parser.FragmentDeclaration:
				fn := record(t)
				// A nested fragment renders at its declaration site.
				curr.edges = append(curr.edges, edgeRef{targetPkg: pkgPath, name: t.Name, pos: pos(t.Range)})
				walkScoped(t.Children, fn)
				continue
			case *parser.TemplElementExpression:
				visitRef(t.Expression.Value, len(t.Children) > 0, t.Expression.Range, curr)
			case *parser.CallTemplateExpression:
				// Legacy "{! Comp(x) }" syntax references components too.
				visitRef(t.Expression.Value, false, t.Expression.Range, curr)
			}
			if c, ok := n.(parser.CompositeNode); ok {
				walkScoped(c.ChildNodes(), curr)
			}
		}
	}
	for _, node := range file.Nodes {
		switch t := node.(type) {
		case *parser.FragmentDeclaration:
			walkScoped(t.Children, record(t))
		case *parser.HTMLTemplate:
			n := &declNode{
				name:    templateName(t.Expression.Value),
				kind:    "templ",
				pkgPath: pkgPath,
				pos:     pos(t.Range),
				params:  signatureParams(t.Expression.Value),
			}
			nodes = append(nodes, n)
			walkScoped(t.Children, n)
		}
	}

	return &fileFragments{decls: decls, refs: refs, nodes: nodes}
}

type fileFragments struct {
	decls []FragmentInfo
	refs  []fragmentRef
	nodes []*declNode
}

// parseFragmentRef classifies a templ-element expression as a candidate
// fragment reference: a bare identifier, a selector, or a call on either.
func parseFragmentRef(exprValue string) (fragmentRef, bool) {
	expr, err := goparser.ParseExpr(exprValue)
	if err != nil {
		return fragmentRef{}, false
	}
	// Explicit generic instantiations ("List[int]" / "Pair[K, V]") wrap
	// the referenced symbol in an index expression; unwrap to it.
	unwrapIndex := func(e goast.Expr) goast.Expr {
		switch t := e.(type) {
		case *goast.IndexExpr:
			return t.X
		case *goast.IndexListExpr:
			return t.X
		}
		return e
	}
	expr = unwrapIndex(expr)
	argCount := -1
	spread := false
	if call, ok := expr.(*goast.CallExpr); ok {
		argCount = len(call.Args)
		spread = call.Ellipsis.IsValid()
		expr = unwrapIndex(call.Fun)
	}
	switch e := expr.(type) {
	case *goast.Ident:
		return fragmentRef{name: e.Name, argCount: argCount, spread: spread}, true
	case *goast.SelectorExpr:
		pkgIdent, ok := e.X.(*goast.Ident)
		if !ok {
			return fragmentRef{}, false
		}
		return fragmentRef{qualifier: pkgIdent.Name, name: e.Sel.Name, argCount: argCount, spread: spread}, true
	}
	return fragmentRef{}, false
}

// countParams counts the declared parameters of a fragment signature like
// "Name(u User, n int)" and reports whether the last one is variadic.
// Grouped parameters count individually. Returns -1 when the signature
// cannot be parsed (checks are then skipped).
func countParams(signature string) (count int, variadic bool) {
	i := strings.IndexByte(signature, '(')
	if i < 0 {
		return -1, false
	}
	expr, err := goparser.ParseExpr("func" + signature[i:])
	if err != nil {
		return -1, false
	}
	ft, ok := expr.(*goast.FuncType)
	if !ok || ft.Params == nil {
		return -1, false
	}
	for _, field := range ft.Params.List {
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		count += n
		_, variadic = field.Type.(*goast.Ellipsis)
	}
	return count, variadic
}

// Fragments returns the whole-set fragment registry keyed by package path
// then name. Used by fragment code generation and reference resolution.
func (s *SetAnalysis) Fragments() map[string]map[string]FragmentInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fragmentRegistryLocked()
}

func (s *SetAnalysis) fragmentRegistryLocked() map[string]map[string]FragmentInfo {
	out := map[string]map[string]FragmentInfo{}
	var fileNames []string
	for name := range s.fragments {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, fn := range fileNames {
		for _, d := range s.fragments[fn].decls {
			if out[d.PkgPath] == nil {
				out[d.PkgPath] = map[string]FragmentInfo{}
			}
			// First declaration wins deterministically; duplicates are
			// reported per file (E0301) and package-wide by codegen.
			if _, exists := out[d.PkgPath][d.Name]; !exists {
				out[d.PkgPath][d.Name] = d
			}
		}
	}
	return out
}

// checkFragmentRefs resolves every candidate reference against the
// registry (FR-032): cross-package references to unexported fragments,
// wrong argument arity, and uncalled fragment references are GHTMX-E0303
// errors naming the fragment. References that target no known fragment are
// ordinary component calls and produce no diagnostic.
func (s *SetAnalysis) checkFragmentRefs(sink *diag.Sink) {
	s.mu.Lock()
	registry := s.fragmentRegistryLocked()
	var refs []fragmentRef
	var decls []FragmentInfo
	var fileNames []string
	for name := range s.fragments {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, fn := range fileNames {
		refs = append(refs, s.fragments[fn].refs...)
		decls = append(decls, s.fragments[fn].decls...)
	}
	s.mu.Unlock()

	// Package-wide duplicate fragments across files: the generated entry
	// points would collide (GHTMX-E0301). Only a file's first declaration
	// of a name is compared — further same-file duplicates are already
	// reported by ValidateFragments.
	firstSeen := map[string]diag.Position{}
	seenInFile := map[string]bool{}
	for _, d := range decls {
		key := d.PkgPath + "\x00" + d.Name
		fileKey := d.Pos.File + "\x00" + key
		if seenInFile[fileKey] {
			continue
		}
		seenInFile[fileKey] = true
		if first, dup := firstSeen[key]; dup {
			sink.Add(diag.DuplicateFragment, d.Pos,
				fmt.Sprintf("duplicate fragment %q in package %s: first declared at %s", d.Name, d.PkgPath, first),
				"rename one of the fragments; generated entry points are package-scoped")
			continue
		}
		firstSeen[key] = d.Pos
	}

	for _, ref := range refs {
		if ref.shadowed {
			// The name refers to a parameter of the enclosing
			// declaration, not to the like-named fragment.
			continue
		}
		targetPkg := ref.filePkg
		display := ref.name
		if ref.qualifier != "" {
			resolved, ok := ref.imports[ref.qualifier]
			if !ok {
				continue // Unknown alias: the Go compiler reports it.
			}
			targetPkg = resolved
			display = ref.qualifier + "." + ref.name
		}
		info, ok := registry[targetPkg][ref.name]
		if !ok {
			continue // Not a fragment: an ordinary component reference.
		}
		if ref.hasChildren {
			sink.Add(diag.FragmentChildren, ref.pos,
				fmt.Sprintf("fragment %s takes no children block: a standalone fragment render has no caller to supply children", display),
				"pass the content as a typed parameter instead")
		}
		if ref.qualifier != "" && targetPkg != ref.filePkg && !info.Exported() {
			first, size := utf8.DecodeRuneInString(ref.name)
			sink.Add(diag.UnresolvableFragment, ref.pos,
				fmt.Sprintf("fragment %s is unexported and cannot be referenced from another package (declared at %s)", display, info.Pos),
				fmt.Sprintf("export it by renaming it to %s", string(unicode.ToUpper(first))+ref.name[size:]))
			continue
		}
		if ref.argCount == -1 {
			hint := fmt.Sprintf("write @%s(...) with the fragment's declared parameters", display)
			if info.ParamCount >= 0 {
				sink.Add(diag.UnresolvableFragment, ref.pos,
					fmt.Sprintf("fragment %s must be called with %d argument(s)", display, info.ParamCount), hint)
				continue
			}
			sink.Add(diag.UnresolvableFragment, ref.pos,
				fmt.Sprintf("fragment %s must be called", display), hint)
			continue
		}
		if info.ParamCount < 0 || ref.spread {
			continue // Unparseable signature or spread call: the Go compiler checks arity.
		}
		if info.Variadic {
			if ref.argCount < info.ParamCount-1 {
				sink.Add(diag.UnresolvableFragment, ref.pos,
					fmt.Sprintf("fragment %s takes at least %d argument(s), got %d (declared at %s)", display, info.ParamCount-1, ref.argCount, info.Pos),
					"the argument list matches the fragment's declared parameters in order")
			}
			continue
		}
		if ref.argCount != info.ParamCount {
			sink.Add(diag.UnresolvableFragment, ref.pos,
				fmt.Sprintf("fragment %s takes %d argument(s), got %d (declared at %s)", display, info.ParamCount, ref.argCount, info.Pos),
				"the argument list matches the fragment's declared parameters in order")
		}
	}
}
