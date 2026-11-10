package codex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MoeForge/moego/ojson"
	"golang.org/x/sync/singleflight"
)

type receiptContextJournalFork struct {
	memoryRefreshJournal
	completed bool
}

func (j *receiptContextJournalFork) RefreshComplete(ctx context.Context, key string, receipt []byte) error {
	if errContext := ctx.Err(); errContext != nil {
		return errContext
	}
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.After(time.Now()) {
		return errors.New("receipt context has no independent future deadline")
	}
	j.completed = true
	return j.memoryRefreshJournal.RefreshComplete(ctx, key, receipt)
}

func TestRefreshReceiptOutlivesExchangeCancellationFork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	journal := &receiptContextJournalFork{}
	want := &CodexTokenData{AccessToken: "test-access", RefreshToken: "test-next", Expire: time.Now().Add(time.Hour).Format(time.RFC3339)}
	got, errRefresh := journalRefresh(ctx, journal, ClientID, "test-grant", func() (*CodexTokenData, error) {
		// Model exchange completion at its deadline without relying on timers.
		cancel()
		return want, nil
	}, decodeRefreshReceipt)
	if errRefresh != nil || got != want || !journal.completed {
		t.Fatalf("successful exchange lost after cancellation: got=%v error=%v completed=%v", got, errRefresh, journal.completed)
	}
}

func TestSharedRefreshReceiptPersistsEveryJournalFork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := &CodexTokenData{AccessToken: "test-access", RefreshToken: "test-next", Expire: time.Now().Add(time.Hour).Format(time.RFC3339)}
	// Both callers receive the same singleflight value but have different stores.
	for _, journal := range []*memoryRefreshJournal{{}, {}} {
		got, errReceipt := journalSharedRefreshReceipt(ctx, journal, ClientID, "test-grant", want, decodeRefreshReceipt)
		if errReceipt != nil || got != want {
			t.Fatalf("shared result not persisted: got=%v error=%v", got, errReceipt)
		}
		replayed, errReplay := journalRefresh(context.Background(), journal, ClientID, "test-grant", func() (*CodexTokenData, error) {
			t.Fatal("saved one-use grant was sent again")
			return nil, nil
		}, decodeRefreshReceipt)
		if errReplay != nil || replayed.RefreshToken != want.RefreshToken {
			t.Fatalf("late follower cannot recover receipt: got=%v error=%v", replayed, errReplay)
		}
	}
}

func TestSharedRefreshReceiptDoesNotOverwritePendingFork(t *testing.T) {
	journal := &memoryRefreshJournal{}
	key := refreshGrantKey(ClientID, "test-pending")
	if claimed, errClaim := journal.RefreshClaim(context.Background(), key); !claimed || errClaim != nil {
		t.Fatalf("claim: %v, %v", claimed, errClaim)
	}
	want := &CodexTokenData{AccessToken: "test-access", RefreshToken: "test-next", Expire: time.Now().Add(time.Hour).Format(time.RFC3339)}
	_, errReceipt := journalSharedRefreshReceipt(context.Background(), journal, ClientID, "test-pending", want, decodeRefreshReceipt)
	if !IsRefreshOutcomeUnknown(errReceipt) {
		t.Fatalf("pending claim overwritten: %v", errReceipt)
	}
	receipt, errRead := journal.RefreshRead(context.Background(), key)
	if errRead != nil || ojson.ParseBytes(receipt).Get("stage").String() != "pending" {
		t.Fatalf("pending receipt changed: %s, %v", receipt, errRead)
	}
}

func TestSharedRefreshCallersKeepDistinctJournalsFork(t *testing.T) {
	for _, siwc := range []bool{false, true} {
		name := "codex"
		if siwc {
			name = "siwc"
		}
		t.Run(name, func(t *testing.T) {
			resetCodexRefreshGroupForTest()
			siwcRefreshGroup = singleflight.Group{}
			t.Cleanup(func() {
				resetCodexRefreshGroupForTest()
				siwcRefreshGroup = singleflight.Group{}
			})
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				var calls atomic.Int32
				transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls.Add(1)
					<-release
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"access_token":"test-access","refresh_token":"test-next","expires_in":3600}`)),
						Request:    req,
					}, nil
				})
				journals := []*memoryRefreshJournal{{}, {}}
				auths := []*CodexAuth{
					{httpClient: &http.Client{Transport: transport}, refreshJournal: journals[0]},
					{httpClient: &http.Client{Transport: transport}, refreshJournal: journals[1]},
				}
				refresh := func(auth *CodexAuth) error {
					if siwc {
						_, errRefresh := auth.RefreshSIWCTokens(context.Background(), "test-shared", "test-client", "test-device")
						return errRefresh
					}
					_, errRefresh := auth.RefreshTokens(context.Background(), "test-shared")
					return errRefresh
				}
				results := make(chan error, 2)
				go func() { results <- refresh(auths[0]) }()
				synctest.Wait()
				go func() { results <- refresh(auths[1]) }()
				// Both callers are durably blocked: the leader in transport,
				// and the follower inside singleflight, before releasing either.
				synctest.Wait()
				close(release)
				synctest.Wait()
				for range auths {
					if errRefresh := <-results; errRefresh != nil {
						t.Fatal(errRefresh)
					}
				}
				for _, auth := range auths {
					if errRefresh := refresh(auth); errRefresh != nil {
						t.Fatalf("late caller lost its journal receipt: %v", errRefresh)
					}
				}
				if got := calls.Load(); got != 1 {
					t.Fatalf("provider consumed shared grant %d times, want 1", got)
				}
			})
		})
	}
}
