# @cloudy/sdk

Manage Cloudy from Node.js or TypeScript: servers, apps, deploys, env, domains, proxy hosts, databases.

```ts
import { Cloudy } from "@cloudy/sdk"

const cloudy = new Cloudy({ url: process.env.CLOUDY_URL!, token: process.env.CLOUDY_TOKEN! })
await cloudy.deploy("shop", { wait: true })
await cloudy.addProxyHost("edge", "nas.example.com", "10.0.0.5:5000")
console.log(await cloudy.call("list_databases"))   // any tool — see `await cloudy.tools()`
```

The token is a team MCP token (`cld_…`, **MCP tokens** in the dashboard). Its scopes decide what the
SDK may do; every call is audited, and destructive tools follow the team's approval setting.
