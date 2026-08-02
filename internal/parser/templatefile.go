package parser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/a-h/parse"
)

func Parse(fileName string) (*TemplateFile, error) {
	fc, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}
	return ParseString(string(fc))
}

func getDefaultPackageName(fileName string) (pkg string) {
	parent := filepath.Base(filepath.Dir(fileName))
	if !isGoIdentifier(parent) {
		return "main"
	}
	return parent
}

func isGoIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if unicode.IsLetter(r) || r == '_' {
			continue
		}
		if i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func ParseString(template string) (*TemplateFile, error) {
	tf, matched, err := NewTemplateFileParser("main").Parse(parse.NewInput(template))
	if err != nil {
		return tf, err
	}
	if !matched {
		err = ErrTemplateNotFound
	}
	return tf, err
}

// NewTemplateFileParser creates a new TemplateFileParser.
func NewTemplateFileParser(pkg string) TemplateFileParser {
	return TemplateFileParser{
		DefaultPackage: pkg,
	}
}

var ErrLegacyFileFormat = errors.New("legacy file format - run templ migrate")
var ErrTemplateNotFound = errors.New("template not found")

type TemplateFileParser struct {
	DefaultPackage string
}

var legacyPackageParser = parse.String("{% package")

func (p TemplateFileParser) Parse(pi *parse.Input) (tf *TemplateFile, matched bool, err error) {
	// If we're parsing a legacy file, complain that migration needs to happen.
	_, matched, err = legacyPackageParser.Parse(pi)
	if err != nil {
		return
	}
	if matched {
		return tf, false, ErrLegacyFileFormat
	}

	// Read until the package.
	tf = &TemplateFile{}
	for {
		// Package.
		// package name
		from := pi.Position()
		tf.Package, matched, err = pkg.Parse(pi)
		if err != nil {
			return
		}
		if matched {
			break
		}

		var line string
		line, matched, err = stringUntilNewLine.Parse(pi)
		if err != nil {
			return
		}
		if !matched {
			break
		}
		var newLine string
		newLine, _, _ = parse.NewLine.Parse(pi)
		tf.Header = append(tf.Header, &TemplateFileGoExpression{Expression: NewExpression(line+newLine, from, pi.Position()), BeforePackage: true})
	}

	// Strip any whitespace between the template declaration and the first template.
	_, _, _ = parse.OptionalWhitespace.Parse(pi)

outer:
	for {
		// Optional templates, CSS, and script templates.
		// templ Name(p Parameter)
		var tn *HTMLTemplate
		tn, matched, err = template.Parse(pi)
		if err != nil {
			tf.Nodes = append(tf.Nodes, tn)
			return tf, false, err
		}
		if matched {
			tf.Nodes = append(tf.Nodes, tn)
			_, _, _ = parse.OptionalWhitespace.Parse(pi)
			continue
		}

		// css Name()
		var cn *CSSTemplate
		cn, matched, err = cssParser.Parse(pi)
		if err != nil {
			return tf, false, err
		}
		if matched {
			tf.Nodes = append(tf.Nodes, cn)
			_, _, _ = parse.OptionalWhitespace.Parse(pi)
			continue
		}

		// script Name()
		var sn *ScriptTemplate
		sn, matched, err = scriptTemplateParser.Parse(pi)
		if err != nil {
			return tf, false, err
		}
		if matched {
			tf.Nodes = append(tf.Nodes, sn)
			_, _, _ = parse.OptionalWhitespace.Parse(pi)
			continue
		}

		// event Name(typed params...)
		var en *EventDeclaration
		en, matched, err = eventDeclaration.Parse(pi)
		if err != nil {
			if en != nil {
				tf.Nodes = append(tf.Nodes, en)
			}
			return tf, false, err
		}
		if matched {
			tf.Nodes = append(tf.Nodes, en)
			_, _, _ = parse.OptionalWhitespace.Parse(pi)
			continue
		}

		// fragment Name()
		var fn Node
		fn, matched, err = fragmentDeclaration.Parse(pi)
		if err != nil {
			if fd, ok := fn.(*FragmentDeclaration); ok && fd != nil {
				fd.TopLevel = true
				tf.Nodes = append(tf.Nodes, fd)
			}
			return tf, false, err
		}
		if matched {
			fd := fn.(*FragmentDeclaration)
			fd.TopLevel = true
			tf.Nodes = append(tf.Nodes, fd)
			_, _, _ = parse.OptionalWhitespace.Parse(pi)
			continue
		}

		// Anything that isn't template content is Go code.
		code := new(strings.Builder)
		from := pi.Position()
	inner:
		for {
			// Check to see if this line isn't Go code.
			last := pi.Index()
			var l string
			if l, matched, err = stringUntilNewLineOrEOF.Parse(pi); err != nil {
				return
			}
			hasTemplatePrefix := strings.HasPrefix(l, "templ ") || strings.HasPrefix(l, "css ") || strings.HasPrefix(l, "script ") || strings.HasPrefix(l, "fragment ") || strings.HasPrefix(l, "event ")
			if hasTemplatePrefix && strings.Contains(l, "(") {
				// Unread the line.
				pi.Seek(last)
				// Take the code so far.
				if code.Len() > 0 {
					raw := code.String()
					expr := NewExpression(strings.TrimSpace(raw), from, pi.Position())
					// Whether the author left a blank line before the
					// declaration that follows is theirs to decide, so it
					// is recorded before TrimSpace discards it: a comment
					// written against a declaration stays against it, and
					// one written as a section heading keeps its gap.
					tf.Nodes = append(tf.Nodes, &TemplateFileGoExpression{
						Expression:     expr,
						BlankLineAfter: endsWithBlankLine(raw),
					})
				}
				// Carry on parsing.
				break inner
			}
			code.WriteString(l)

			// Eat the newline or EOF that we read until.
			var newLine string
			if newLine, matched, err = parse.NewLine.Parse(pi); err != nil {
				return
			}
			code.WriteString(newLine)
			if _, isEOF, _ := parse.EOF[string]().Parse(pi); isEOF {
				if code.Len() > 0 {
					raw := code.String()
					expr := NewExpression(strings.TrimSpace(raw), from, pi.Position())
					tf.Nodes = append(tf.Nodes, &TemplateFileGoExpression{
						Expression:     expr,
						BlankLineAfter: endsWithBlankLine(raw),
					})
				}
				// Stop parsing.
				break outer
			}
		}
	}

	return tf, true, nil
}

// endsWithBlankLine reports whether the raw source of a top-level Go
// expression was followed by an empty line, which is how the author
// separated it from whatever declaration comes next.
func endsWithBlankLine(raw string) bool {
	// Inspect the last two lines rather than matching suffixes: a
	// separator line holding spaces or tabs ("// c\n   \n") is still a
	// blank line to the author, and suffix matching would miss it and
	// glue the comment to the next declaration — the bug this whole
	// field exists to prevent.
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return false
	}
	return strings.TrimSpace(lines[len(lines)-1]) == "" &&
		strings.TrimSpace(lines[len(lines)-2]) == ""
}
