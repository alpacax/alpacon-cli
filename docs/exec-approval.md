# Exec approval waits

There are two approval paths. A status-hold job is already parked in
`awaiting_approval`; `alpacon exec --wait` resubscribes to that same job and
streams its output after approval. A sudo denial has already run and been
blocked, so the CLI polls that command's `sudo_grant_status` and runs the
command once after authorization. Retrying the denial on each tick would
create duplicate approval jobs and could repeat side effects.

The wait keeps the original pending error when a status read cannot provide an
answer. Network and decoding failures back off and eventually give up on the
pending contract; HTTP 429 responses use the throttle budget and do not count
toward that failure cap. The budget can extend the deadline while the wait is
within its configured bound. Successful reads reset consecutive failures, but
a still pending status does not refill the throttle allowance.

The sudo hint table mirrors the server's denial codes manually; unknown codes
fall back to a generic hint. Alpamon sanitizes codes to `[A-Z0-9_]`, and hints
describe the denial category without exposing risk scores or reasoning.
`pendingApproval` and `selfService` stay beside each hint so guidance and
approval classification cannot drift into separate lists. Self-service hints
take precedence among pending denials because the pending message already
explains how to wait for a reviewer. Server gate codes keep their evaluation
order at the front of the table; the remaining entries are alternative verdicts.
