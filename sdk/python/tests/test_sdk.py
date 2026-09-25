import io
import json
import unittest
import urllib.error

from hosbah import Hosbah, HosbahApiError, HosbahToolError


class FakeResp(io.BytesIO):
    def __init__(self, status, body):
        super().__init__(json.dumps(body).encode())
        self.status = status

    def __enter__(self):
        return self

    def __exit__(self, *a):
        return False


class FakeOpener:
    def open(self, req, timeout=None):
        body = json.loads(req.data)
        if req.headers.get("Authorization") != "Bearer hbu_ok":
            raise urllib.error.HTTPError(req.full_url, 401, "Unauthorized", {}, io.BytesIO(b'{"error":{"message":"invalid token"}}'))
        args = body["params"].get("arguments", {})
        if body["params"].get("name") == "deploy_app" and args.get("app") == "shop":
            return FakeResp(200, {"result": {"content": [{"type": "text", "text": "Deployment #3 succeeded."}]}})
        return FakeResp(200, {"result": {"content": [{"type": "text", "text": "app not found"}], "isError": True}})


class SDKTest(unittest.TestCase):
    def test_calls(self):
        c = Hosbah("https://c.test/", "hbu_ok", opener=FakeOpener())
        self.assertEqual(c.deploy("shop", wait=True), "Deployment #3 succeeded.")
        with self.assertRaises(HosbahToolError):
            c.deploy("nope")
        with self.assertRaises(HosbahApiError) as e:
            Hosbah("https://c.test", "hbu_bad", opener=FakeOpener()).apps()
        self.assertEqual(e.exception.status, 401)
        with self.assertRaises(ValueError):
            Hosbah("https://c.test", "")


class ContinueOpener:
    def __init__(self):
        self.calls = 0

    def open(self, req, timeout=None):
        self.calls += 1
        name = json.loads(req.data)["params"]["name"]
        nxt = 'continue: wait_deployment {"app":"shop","deployment":"4","until":"2026-09-25T12:00:00Z"}\n'
        if name == "deploy_app":
            return FakeResp(200, {"result": {"content": [{"type": "text", "text": "Deploy #4 of shop queued.\n" + nxt}]}})
        if self.calls == 2:
            return FakeResp(200, {"result": {"content": [{"type": "text", "text": "#4 is running.\n" + nxt}]}})
        return FakeResp(200, {"result": {"content": [{"type": "text", "text": "✓ #4 succeeded in 1m2s.\n"}]}})


class ContinueTest(unittest.TestCase):
    def test_follows_continue(self):
        o = ContinueOpener()
        out = Hosbah("https://c.test", "hbu_ok", opener=o).deploy("shop", wait=True)
        self.assertEqual(out, "Deploy #4 of shop queued.\n✓ #4 succeeded in 1m2s.\n")
        self.assertEqual(o.calls, 3)


if __name__ == "__main__":
    unittest.main()
