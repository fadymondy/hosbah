# hosbah-sdk (Python)

```python
import os
from hosbah import Hosbah

hosbah = Hosbah(os.environ["HOSBAH_URL"], os.environ["HOSBAH_TOKEN"])
hosbah.deploy("shop", wait=True)
hosbah.add_proxy_host("edge", "nas.example.com", "10.0.0.5:5000")
print(hosbah.call("list_databases"))   # any tool — see hosbah.tools()
```

Standard library only. The token is a team MCP token (`hbu_…`, or `cld_…` minted before the rename); its scopes decide what the SDK may do.
Run the tests with `python -m unittest discover tests`.
