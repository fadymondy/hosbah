"""Cloudy SDK for Python (3.9+, standard library only).

It calls a team's MCP endpoint (POST /api/mcp) with a team token (cld_…), so every Cloudy tool is
available through ``call``, with the token's scopes, the audit log and destructive-action approvals.

    from cloudy import Cloudy
    cloudy = Cloudy(os.environ["CLOUDY_URL"], os.environ["CLOUDY_TOKEN"])
    cloudy.deploy("shop", wait=True)
"""

from __future__ import annotations

import itertools
import json
import urllib.error
import urllib.request
from typing import Any, Dict, List, Optional

__all__ = ["Cloudy", "CloudyToolError", "CloudyApiError"]
__version__ = "0.1.0"


class CloudyToolError(Exception):
    """A tool refused (not found, missing scope, validation…)."""

    def __init__(self, tool: str, message: str) -> None:
        super().__init__(f"cloudy: {tool}: {message}")
        self.tool = tool
        self.message = message


class CloudyApiError(Exception):
    """Transport or protocol failure (bad token, server error)."""

    def __init__(self, status: int, message: str) -> None:
        super().__init__(f"cloudy: {message} (HTTP {status})")
        self.status = status
        self.message = message


class Cloudy:
    def __init__(self, url: str, token: str, timeout: float = 600.0, opener: Optional[urllib.request.OpenerDirector] = None) -> None:
        self.url = url.rstrip("/")
        self.token = (token or "").strip()
        self.timeout = timeout
        self._open = (opener or urllib.request.build_opener()).open
        self._ids = itertools.count(1)
        if not self.token:
            raise ValueError("cloudy: no token (mint one under MCP tokens)")

    def _rpc(self, method: str, params: Dict[str, Any]) -> Any:
        body = json.dumps({"jsonrpc": "2.0", "id": next(self._ids), "method": method, "params": params}).encode()
        req = urllib.request.Request(
            self.url + "/api/mcp",
            data=body,
            method="POST",
            headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json", "Accept": "application/json"},
        )
        try:
            with self._open(req, timeout=self.timeout) as resp:
                status, raw = resp.status, resp.read()
        except urllib.error.HTTPError as e:
            status, raw = e.code, e.read()
        try:
            data = json.loads(raw)
        except ValueError:
            raise CloudyApiError(status, raw[:200].decode(errors="replace") or "unexpected response") from None
        if data.get("error"):
            raise CloudyApiError(status, data["error"].get("message", "error"))
        if status >= 300:
            raise CloudyApiError(status, "request failed")
        return data.get("result")

    def tools(self) -> List[Dict[str, Any]]:
        """What this token can call."""
        return self._rpc("tools/list", {}).get("tools", [])

    def call(self, tool: str, **args: Any) -> str:
        """Run any tool; returns its text output, raises CloudyToolError on a refusal."""
        r = self._rpc("tools/call", {"name": tool, "arguments": args})
        text = "".join(c.get("text", "") for c in r.get("content", []))
        if r.get("isError"):
            raise CloudyToolError(tool, text)
        return text

    def whoami(self) -> str:
        return self.call("whoami")

    def servers(self) -> str:
        return self.call("list_servers")

    def apps(self) -> str:
        return self.call("list_apps")

    def deploy(self, app: str, wait: bool = False, **args: Any) -> str:
        return self.call("deploy_app", app=app, wait=wait, **args)

    def set_env(self, app: str, name: str, value: str) -> str:
        return self.call("set_env", app=app, name=name, value=value)

    def add_domain(self, app: str, hostname: str, **args: Any) -> str:
        return self.call("add_domain", app=app, hostname=hostname, **args)

    def add_proxy_host(self, server: str, hostname: str, upstream: str, **args: Any) -> str:
        return self.call("add_proxy_host", server=server, hostname=hostname, upstream=upstream, **args)

    def connect_server(self, name: str, host: str, **login: Any) -> str:
        return self.call("add_server", name=name, method="connect", host=host, **login)
