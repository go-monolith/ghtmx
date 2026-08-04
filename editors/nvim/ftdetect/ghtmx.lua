-- Both extensions map to the ghtmx filetype: .ghtmx is canonical, and
-- .htmx is accepted because some projects prefer it.
vim.filetype.add({ extension = { ghtmx = "ghtmx", htmx = "ghtmx" } })
