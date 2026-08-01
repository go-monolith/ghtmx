-- Neovim wiring for the ghtmx language server. The server provides live
-- diagnostics, route-aware completion, hover, and go to definition, and
-- proxies gopls for embedded Go, so this module only starts `ghtmx lsp`
-- for ghtmx buffers.
local M = {}

local function root_dir(bufnr)
  local fname = vim.api.nvim_buf_get_name(bufnr)
  return vim.fs.root(fname, { "ghtmx.json", "go.mod", ".git" })
end

--- Registers the ghtmx filetype and starts the language server for it.
--- @param opts table|nil { cmd?: string[], capabilities?: table, settings?: table }
function M.setup(opts)
  opts = opts or {}
  local cmd = opts.cmd or { "ghtmx", "lsp" }

  vim.filetype.add({ extension = { ghtmx = "ghtmx" } })

  local group = vim.api.nvim_create_augroup("ghtmx_lsp", { clear = true })
  vim.api.nvim_create_autocmd("FileType", {
    group = group,
    pattern = "ghtmx",
    callback = function(args)
      vim.lsp.start({
        name = "ghtmx",
        cmd = cmd,
        root_dir = root_dir(args.buf),
        capabilities = opts.capabilities,
        settings = opts.settings,
      }, { bufnr = args.buf })
    end,
  })
end

return M
