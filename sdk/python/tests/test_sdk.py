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


if __name__ == "__main__":
    unittest.main()
