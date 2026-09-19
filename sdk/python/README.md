# hosbah-sdk (Python)

```python
import os
from hosbah import Hosbah

cloudy = Hosbah(os.environ["CLOUDY_URL"], os.environ["CLOUDY_TOKEN"])
cloudy.deploy("shop", wait=True)
cloudy.add_proxy_host("edge", "nas.example.com", "10.0.0.5:5000")
print(cloudy.call("list_databases"))   # any tool — see cloudy.tools()
```

Standard library only. The token is a team MCP token (`cld_…`); its scopes decide what the SDK may do.
Run the tests with `python -m unittest discover tests`.
