# @hosbah/sdk

Manage Hosbah from Node.js or TypeScript: servers, apps, deploys, env, domains, proxy hosts, databases.

```ts
import { Hosbah } from "@hosbah/sdk"

const hosbah = new Hosbah({ url: process.env.HOSBAH_URL!, token: process.env.HOSBAH_TOKEN! })
await hosbah.deploy("shop", { wait: true })
await hosbah.addProxyHost("edge", "nas.example.com", "10.0.0.5:5000")
console.log(await hosbah.call("list_databases"))   // any tool — see `await hosbah.tools()`
```

The token is a team MCP token (`hbu_…` — or `cld_…` if it was minted before the rename; **MCP tokens** in the dashboard). Its scopes decide what the
SDK may do; every call is audited, and destructive tools follow the team's approval setting.

This repository is published from [fadymondy/hosbah](https://github.com/fadymondy/hosbah) (`sdk/node`); open issues and changes there. MIT licensed.
