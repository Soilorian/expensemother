"""Exercise the built container and PostgreSQL through the public API."""
import json
import urllib.request
import uuid


def call(method, path, body=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(
        "http://127.0.0.1:17008/v1" + path,
        data=json.dumps(body).encode() if body is not None else None,
        headers=headers, method=method,
    )
    with urllib.request.urlopen(request, timeout=15) as response:
        return json.load(response)


session = call("POST", "/auth/signup", {"username": "ci" + uuid.uuid4().hex[:12], "password": "ci-password-123"})
token = session["token"]
group = call("POST", "/groups", {"displayName": "CI group", "users": []}, token)
expense = call("POST", "/expenses", {
    "groupId": group["groupId"], "expenseType": "COST", "state": "DRAFT",
    "displayName": "CI dinner", "amount": 1200000, "payerId": session["userId"],
    "sharers": [{"userId": session["userId"]}],
}, token)
loaded = call("GET", "/expenses/" + expense["expenseId"], token=token)
assert loaded["state"] == "DRAFT" and loaded["amount"] == 1200000
print("Container API + PostgreSQL signup/group/draft/read smoke test passed.")
