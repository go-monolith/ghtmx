# ghtmx for Neovim

Filetype detection, syntax highlighting, and language-server wiring for
`.ghtmx` templates. The server (`ghtmx lsp`) provides live diagnostics,
route-aware completion, hover, and go to definition, and proxies gopls
for the embedded Go.

## Requirements

- Neovim 0.10+
- `ghtmx` and `gopls` on PATH:
  `go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest`
  `go install golang.org/x/tools/gopls@latest`

## Install

With [lazy.nvim](https://github.com/folke/lazy.nvim):

```lua
{
  "go-monolith/ghtmx",
  -- The plugin lives in a subdirectory of the module repository.
  config = function(plugin)
    vim.opt.rtp:append(plugin.dir .. "/editors/nvim")
    require("ghtmx").setup()
  end,
}
```

Or copy `editors/nvim` onto your runtimepath and call:

```lua
require("ghtmx").setup({
  -- cmd = { "ghtmx", "lsp", "-log", "/tmp/ghtmx-lsp.log" },
})
```

`setup` registers the filetype and starts `ghtmx lsp` for ghtmx buffers
via `vim.lsp.start`, rooting the workspace at `ghtmx.json`, `go.mod`, or
`.git`. Diagnostics, completion (LSP omnifunc `<C-x><C-o>` on 0.10, or
`vim.lsp.completion` on 0.11+, or nvim-cmp), hover (`K`), and definition
(`gd` / `<C-]>`) then work out of the box.

### nvim-lspconfig alternative

If you prefer nvim-lspconfig, register a custom server instead of
calling `setup`:

```lua
require("lspconfig.configs").ghtmx = {
  default_config = {
    cmd = { "ghtmx", "lsp" },
    filetypes = { "ghtmx" },
    root_dir = require("lspconfig.util").root_pattern("ghtmx.json", "go.mod", ".git"),
  },
}
require("lspconfig").ghtmx.setup({})
```

## Releasing

The plugin is released with the module: tag the repository and the
plugin ships at that tag. Follow the versioning policy in
[`editors/README.md`](../README.md) — the plugin is tested against the
`ghtmx lsp` of the same MAJOR.MINOR module series.
