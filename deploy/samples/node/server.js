const http = require("http")
const version = "v1"
http
  .createServer((req, res) => {
    if (req.url === "/health") return res.end("ok\n")
    res.end(`hello from Node ${version} (${process.env.GREETING ?? ""})\n`)
  })
  .listen(process.env.PORT || 3000, () => console.log("listening on " + (process.env.PORT || 3000)))
