"""Hosbah SDK for Python (3.9+, standard library only).

It calls a team's MCP endpoint (POST /api/mcp) with a team token (hbu_…, or cld_… minted before the rename), so every Hosbah tool is
available through ``call``, with the token's scopes, the audit log and destructive-action approvals.

    from hosbah import Hosbah
    hosbah = Hosbah(os.environ["HOSBAH_URL"], os.environ["HOSBAH_TOKEN"])
    hosbah.deploy("shop", wait=True)
"""

from __future__ import annotations

import itertools
import json
import urllib.error
import urllib.request
from typing import Any, Dict, List, Optional

__all__ = ["Hosbah", "HosbahToolError", "HosbahApiError"]
__version__ = "0.1.0"


class HosbahToolError(Exception):
    """A tool refused (not found, missing scope, validation…)."""

    def __init__(self, tool: str, message: str) -> None:
        super().__init__(f"hosbah: {tool}: {message}")
        self.tool = tool
        self.message = message


class HosbahApiError(Exception):
    """Transport or protocol failure (bad token, server error)."""

    def __init__(self, status: int, message: str) -> None:
        super().__init__(f"hosbah: {message} (HTTP {status})")
        self.status = status
        self.message = message


class Hosbah:
    def __init__(self, url: str, token: str, timeout: float = 600.0, opener: Optional[urllib.request.OpenerDirector] = None) -> None:
        self.url = url.rstrip("/")
        self.token = (token or "").strip()
        self.timeout = timeout
        self._open = (opener or urllib.request.build_opener()).open
        self._ids = itertools.count(1)
        if not self.token:
            raise ValueError("hosbah: no token (mint one under MCP tokens)")

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
            raise HosbahApiError(status, raw[:200].decode(errors="replace") or "unexpected response") from None
        if data.get("error"):
            raise HosbahApiError(status, data["error"].get("message", "error"))
        if status >= 300:
            raise HosbahApiError(status, "request failed")
        return data.get("result")

    def tools(self) -> List[Dict[str, Any]]:
        """What this token can call."""
        return self._rpc("tools/list", {}).get("tools", [])

    def call(self, tool: str, **args: Any) -> str:
        """Run any tool; returns its text output, raises HosbahToolError on a refusal."""
        r = self._rpc("tools/call", {"name": tool, "arguments": args})
        text = "".join(c.get("text", "") for c in r.get("content", []))
        if r.get("isError"):
            raise HosbahToolError(tool, text)
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
