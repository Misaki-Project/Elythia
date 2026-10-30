package processors_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elythia-network/elythia/internal/activitypub"
	"github.com/elythia-network/elythia/internal/queue"
	"github.com/elythia-network/elythia/internal/queue/driver"
	"github.com/elythia-network/elythia/internal/queue/processors"
	"github.com/stretchr/testify/require"
)

func signedReplayPayloadWithDates(t *testing.T, key *activitypub.PrivateKey, body []byte, date, xDate string, signXDate bool) queue.InboxPayload {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://example.com/inbox", nil)
	req.Host = "example.com"
	req.Header.Set("Date", date)
	req.Header.Set("X-Date", xDate)
	signed := []string{"(request-target)", "date", "host", "digest"}
	if signXDate {
		signed = append(signed, "x-date")
	}
	require.NoError(t, activitypub.SignRequest(req, key, activitypub.SHA256Digest(body), signed))
	headers := map[string]string{"Host": req.Host}
	for _, h := range []string{"Signature", "Date", "X-Date", "Digest"} {
		headers[h] = req.Header.Get(h)
	}
	return queue.InboxPayload{Body: body, Method: req.Method, Path: req.URL.RequestURI(), Headers: headers}
}

// Dateと本文が同じでも、署名されたX-Dateを更新した正当な再配送は通す。
// Dateだけを鍵に戻すと、2件目が再投函として捨てられて落ちる。
func TestInboxProcessor_SameBodyWithNewSignedXDateIsNotAReplay(t *testing.T) {
	body := []byte(`{"id":"https://remote.example/follows/x-date","type":"Follow","actor":"https://remote.example/users/alice","object":"https://local.example/users/bob"}`)
	verifier, key, _ := signedReplayFixtureWithKey(t, body)
	now := time.Now().UTC()
	date := now.Add(-30 * time.Second).Format(http.TimeFormat)
	first := signedReplayPayloadWithDates(t, key, body, date, now.Add(-10*time.Second).Format(http.TimeFormat), true)
	second := signedReplayPayloadWithDates(t, key, body, date, now.Format(http.TimeFormat), true)

	stub := &stubFedProcessor{}
	p := processors.NewInboxProcessor(stub, verifier, nil, nil, nil, newMemReplayGuard())
	require.NoError(t, p.Handle(context.Background(), driver.RawTask{TypeName: queue.TaskTypeInbox, Body: mustEncode(t, first)}))
	require.Len(t, stub.calls, 1, "the first signed delivery must reach dispatch")
	require.NoError(t, p.Handle(context.Background(), driver.RawTask{TypeName: queue.TaskTypeInbox, Body: mustEncode(t, second)}))
	require.Len(t, stub.calls, 2, "a new signed X-Date must distinguish legitimate deliveries with the same Date and body")
}

func TestInboxProcessor_IdenticalSignedXDateRequestIsAReplay(t *testing.T) {
	body := []byte(`{"id":"https://remote.example/follows/x-date","type":"Follow","actor":"https://remote.example/users/alice","object":"https://local.example/users/bob"}`)
	verifier, key, _ := signedReplayFixtureWithKey(t, body)
	now := time.Now().UTC()
	payload := signedReplayPayloadWithDates(t, key, body, now.Add(-30*time.Second).Format(http.TimeFormat), now.Format(http.TimeFormat), true)
	stub := &stubFedProcessor{}
	p := processors.NewInboxProcessor(stub, verifier, nil, nil, nil, newMemReplayGuard())
	task := driver.RawTask{TypeName: queue.TaskTypeInbox, Body: mustEncode(t, payload)}
	require.NoError(t, p.Handle(context.Background(), task))
	require.Len(t, stub.calls, 1, "the signed request must reach dispatch before testing its replay")
	require.NoError(t, p.Handle(context.Background(), task))
	require.Len(t, stub.calls, 1, "an identical signed X-Date request must not reach dispatch twice")
}

// 署名されていないX-Dateは捕まえたリクエストに後付けできるため、鍵へ入れない。
func TestInboxProcessor_UnsignedXDateDoesNotBypassReplayGuard(t *testing.T) {
	body := []byte(`{"id":"https://remote.example/follows/x-date","type":"Follow","actor":"https://remote.example/users/alice","object":"https://local.example/users/bob"}`)
	verifier, key, _ := signedReplayFixtureWithKey(t, body)
	now := time.Now().UTC()
	payload := signedReplayPayloadWithDates(t, key, body, now.Add(-30*time.Second).Format(http.TimeFormat), now.Add(-10*time.Second).Format(http.TimeFormat), false)
	stub := &stubFedProcessor{}
	p := processors.NewInboxProcessor(stub, verifier, nil, nil, nil, newMemReplayGuard())
	require.NoError(t, p.Handle(context.Background(), driver.RawTask{TypeName: queue.TaskTypeInbox, Body: mustEncode(t, payload)}))
	require.Len(t, stub.calls, 1, "the request with an unsigned X-Date must reach dispatch")
	payload.Headers["X-Date"] = now.Format(http.TimeFormat)
	require.NoError(t, p.Handle(context.Background(), driver.RawTask{TypeName: queue.TaskTypeInbox, Body: mustEncode(t, payload)}))
	require.Len(t, stub.calls, 1, "changing only an unsigned X-Date must not bypass the replay guard")
}
