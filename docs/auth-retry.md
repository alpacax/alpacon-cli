# Authentication retry behavior

The client can refresh and replay a request when the response matches the
existing authentication retry heuristic. The heuristic requires HTTP 401, a
JSON-object error body, and no structured error code. It applies only when the
rejected request carried a `Bearer` token. Legacy API keys and service tokens
do not have a refresh-token grant available through this path.

The JSON shape is a filter, not proof of origin. A proxy, WAF, mTLS gateway, or
other intermediary can also return a JSON object. Conversely, a non-JSON 401 is
treated as a response that this client cannot repair with a new token. A
code-less 401 is also not proof that the credential expired. The current API
protocol leaves this narrow case as the useful retry candidate because coded
responses identify a refusal such as MFA, IP policy, or token ACL; refreshing a
token does not inherently resolve those conditions. This protocol rationale is
qualified by the current server behavior and should be revisited if the error
contract changes.

Each request is retried at most once. A retry is made only when the request body
can be replayed: `net/http` must provide `GetBody`, or the request must have no
body. A streamed body without a rewind function is returned with the original
error rather than sent again partially.

Replay relies on the API rejecting authentication before the requested
operation executes. It must not be extended to arbitrary network failures or
other responses that could follow a completed operation. Keeping renewal in
the shared client also covers long-running approval waits whose startup token
expires after the client has been created.

Refresh grants are serialized by `refreshMu`. The rejected token is compared
with the current token while holding that lock: if another overlapping request
already refreshed it, the current request reuses the new token. Only successful
overlapping refreshes coalesce. A failed refresh leaves the old token in place,
so another request that saw the same 401 may attempt its own grant. The
per-request retry limit still bounds each caller's replay.

`refreshMu` is separate from `tokenMu`. Refreshing performs network I/O and
must not hold the short-lived token read/write lock while that work is in
progress; requests need to keep reading the current token while a grant runs.
