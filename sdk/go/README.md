# hosbah-go

The Hosbah SDK for Go: manage servers, apps, deploys, env, domains, proxy hosts and databases on [Hosbah](https://hosbah.com) from your program.

```sh
go get github.com/fadymondy/hosbah-go
```

```go
import hosbah "github.com/fadymondy/hosbah-go"

c := hosbah.New(os.Getenv("HOSBAH_URL"), os.Getenv("HOSBAH_TOKEN"))
out, err := c.Call(ctx, "list_apps", nil) // any tool the token may call — see c.Tools(ctx)
```

The token is a team MCP token (`hbu_…`, or `cld_…` if it was minted before the rename), created under **MCP tokens** in the Hosbah dashboard. Its scopes decide what the SDK may do; every call is audited, and destructive tools follow the team's approval setting.

This repository is published from [fadymondy/hosbah](https://github.com/fadymondy/hosbah) (`pkg/hosbah`); open issues and changes there. MIT licensed.
