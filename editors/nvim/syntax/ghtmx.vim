" Syntax highlighting for ghtmx templates: Go-style top level plus the
" ghtmx-native constructs — templ/fragment/event declarations, htmx
" attributes, and route bindings. Declaration and control keywords are
" anchored to the start of the line so prose inside markup (\"for more
" information\") is never highlighted as code.
if exists("b:current_syntax")
  finish
endif

" Declarations: templ Page(, fragment UserRow(, event ItemSaved(, and
" method templates: templ (r Row) Render(.
syn match ghtmxDeclaration "^\s*\%(templ\|fragment\|event\|css\|script\)\>"
syn match ghtmxDeclName "\%(^\s*\%(templ\|fragment\|event\|css\|script\)\s\+\%((\_[^)]*)\s\+\)\?\)\@<=[A-Za-z_][A-Za-z0-9_]*"

" Go top level and control flow, anchored like gofmt lays them out.
syn match ghtmxGoKeyword "^\%(package\|import\|var\|const\|func\|type\)\>"
syn match ghtmxControl "^\s*\%(}\s*\)\?\%(if\|else\|for\|switch\|case\|default\)\>"
syn keyword ghtmxGoOperator range return

" Component and fragment references: @Layout, @pkg.UserRow.
syn match ghtmxComponent "@[A-Za-z_][A-Za-z0-9_.]*"

" HTML tags and attributes.
syn match ghtmxTag "</\?[A-Za-z][A-Za-z0-9-]*" contains=ghtmxTagPunct
syn match ghtmxTagPunct "</\?" contained
syn match ghtmxAttribute "\<[a-zA-Z_][a-zA-Z0-9:._-]*\ze=" containedin=ALLBUT,ghtmxComment,ghtmxBlockComment,ghtmxHTMLComment,ghtmxString

" htmx attributes win over the generic attribute match.
syn match ghtmxHtmxAttr "\<hx-[a-zA-Z0-9:._-]\+"

" Route bindings and Go interpolations: hx-post={ handlers.CreateItem },
" { user.Name }. The braces are highlighted; the expression stays plain.
syn match ghtmxBindingBrace "={\|[{}]"

" Strings and literals.
syn region ghtmxString start=+"+ skip=+\\"+ end=+"+ oneline
syn region ghtmxString start=+'+ skip=+\\'+ end=+'+ oneline
syn region ghtmxString start=+`+ end=+`+
syn match ghtmxNumber "\<\d\+\%(\.\d\+\)\?\>"

" Comments.
syn match ghtmxComment "//.*$"
syn region ghtmxBlockComment start="/\*" end="\*/"
syn region ghtmxHTMLComment start="<!--" end="-->"

hi def link ghtmxDeclaration Keyword
hi def link ghtmxGoKeyword Keyword
hi def link ghtmxGoOperator Keyword
hi def link ghtmxControl Conditional
hi def link ghtmxDeclName Function
hi def link ghtmxComponent Function
hi def link ghtmxTag Tag
hi def link ghtmxTagPunct Delimiter
hi def link ghtmxAttribute Identifier
hi def link ghtmxHtmxAttr Special
hi def link ghtmxBindingBrace Delimiter
hi def link ghtmxString String
hi def link ghtmxNumber Number
hi def link ghtmxComment Comment
hi def link ghtmxBlockComment Comment
hi def link ghtmxHTMLComment Comment

let b:current_syntax = "ghtmx"
