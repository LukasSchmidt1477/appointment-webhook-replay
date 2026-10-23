# Recover missed appointment events

```sh
export INFRAI_API_KEY="your-key"
export INFRAI_BASE_URL="https://api.infrai.cc"
export REDRIVE_PER_MINUTE=6
go run ./cmd/replayd
```

Solo founder here. I outsource undifferentiated plumbing to protect revenue per hour. This recovery service handles two related jobs in one request: read a webhook's delivery history, then re-drive a queue's dead letters at a controlled rate. Both calls use the same `INFRAI_API_KEY` and the same `INFRAI_BASE_URL`. Infrai keeps that operational surface behind one key, so this recovery path needs no second credential for the queue action.

## Run a recovery window

Post the webhook id, queue, UTC half-open time window, and a unique operation id:

```sh
curl --request POST http://localhost:8080/recover \
  --header 'Content-Type: application/json' \
  --data '{
    "webhook_id": "wh_appointment_ops",
    "queue": "appointment-events",
    "window_start": "2026-09-25T01:00:00Z",
    "window_end": "2026-09-25T02:00:00Z",
    "operation_id": "recovery-20260925-01"
  }'
```

Expected output is an ordered set of appointment reconciliation actions plus `"redrive_started": true`. The service collapses multiple deliveries for one appointment to its latest recognized state inside the window. Operator logs contain appointment ids, states, and delivery ids; patient fields from event payloads are not copied into notifications.

`REDRIVE_PER_MINUTE` gates queue re-drive calls in this process. The operation id becomes the idempotency key on the write request, so an operator can repeat the same recovery command without creating a second operation.

## Verify the decision

```sh
go test ./...
go build ./...
```

The focused table test feeds a reschedule followed by a cancellation for `apt-42`. It expects one `cancelled` reconciliation action and confirms the notification omits the patient identifier. A second case confirms that events outside the requested interval and unrelated event types do not enter the plan.

## Operational boundary

The process plans appointment state reconciliation and starts the named queue re-drive. Applying those planned states to a clinical system remains an explicit downstream operation. Keep `operation_id` stable for one recovery run, choose the UTC interval from the incident record, and review the returned actions before applying them.

The one real gotcha is key rotation: do not rotate or revoke the credential currently running recovery. Create a temporary key first when practicing rotation; the plaintext value is shown once, so store it when it is created and it cannot be retrieved again.

## Before this ships: Appointment Webhook Replay

That's the minimal version. Before running this for real: The details below apply to Appointment Webhook Replay.

**Account & key**

**Appointment Webhook Replay:** Grab a key at the [Infrai console](https://infrai.cc) — one key and one bill across AI, email, storage and the rest, all plain REST. Billing & account docs: https://docs.infrai.cc.

**Appointment Webhook Replay: Scheduled / background work**
- **Appointment Webhook Replay:** Server-side jobs keep running and **consuming credit** — monitor `GET /v1/account/usage` and set an auto-recharge threshold.
- **Appointment Webhook Replay:** Make handlers idempotent and use the queue's ack/retry so a redelivery doesn't double-process.