// SPDX-FileCopyrightText: syuilo and misskey-project
// SPDX-License-Identifier: AGPL-3.0-only

package fxtwitterpreview

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestMediaAdmissionBoundsConcurrentDecodesAndAcceptedRequests(t *testing.T) {
	admission := newMediaAdmission(1, 2, 2048)
	firstReservation, err := admission.admit(context.Background(), 512)
	if err != nil {
		t.Fatal(err)
	}
	firstDecoder, err := admission.acquireDecoder(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	type releases struct{ reservation, decoder func() }
	second := make(chan releases, 1)
	secondErr := make(chan error, 1)
	go func() {
		reservation, err := admission.admit(context.Background(), 512)
		if err != nil {
			secondErr <- err
			return
		}
		decoder, err := admission.acquireDecoder(context.Background())
		if err != nil {
			reservation()
			secondErr <- err
			return
		}
		second <- releases{reservation, decoder}
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := admission.admit(context.Background(), 512); !errors.Is(err, errMediaBusy) {
		t.Fatalf("third request error = %v, want busy", err)
	}
	firstDecoder()
	firstReservation()
	select {
	case got := <-second:
		got.decoder()
		got.reservation()
	case err := <-secondErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("second request did not acquire released capacity")
	}
}

func TestMediaAdmissionRejectsImpossibleBufferReservation(t *testing.T) {
	admission := newMediaAdmission(1, 1, 1024)
	if _, err := admission.admit(context.Background(), 1024); !errors.Is(err, errMediaBusy) {
		t.Fatalf("oversized reservation error = %v, want busy", err)
	}
}

func TestMediaFetchAdmissionLimitsOutboundRequestsBeforeTransport(t *testing.T) {
	admission := newMediaAdmission(1, 1, 1024)
	started := make(chan struct{})
	continueRequest := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-continueRequest:
			return response(200, "image/png", string(tinyImage(t, "png"))), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	firstResult := make(chan error, 1)
	go func() {
		_, release, err := fetchMediaWithAdmission(context.Background(), client, "https://pbs.twimg.com/a.png", 512, admission)
		if release != nil {
			defer release()
		}
		firstResult <- err
	}()
	<-started
	decoderRelease, err := admission.acquireDecoder(context.Background())
	if err != nil {
		t.Fatalf("slow response occupied decoder slot: %v", err)
	}
	decoderRelease()
	var outbound int
	secondClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		outbound++
		return response(200, "image/png", string(tinyImage(t, "png"))), nil
	})}
	if _, release, err := fetchMediaWithAdmission(context.Background(), secondClient, "https://pbs.twimg.com/b.png", 512, admission); !errors.Is(err, errMediaBusy) || release != nil {
		t.Fatalf("second admitted request = release:%v err:%v; want busy", release != nil, err)
	}
	if outbound != 0 {
		t.Fatalf("busy request reached outbound transport %d times", outbound)
	}
	close(continueRequest)
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first media request did not complete")
	}
}

func TestMediaFetchKeepsExactBufferReservationUntilResponseRelease(t *testing.T) {
	admission := newMediaAdmission(1, 2, 600)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, "image/png", string(tinyImage(t, "png"))), nil
	})}
	_, release, err := fetchMediaWithAdmission(context.Background(), client, "https://pbs.twimg.com/a.png", 512, admission)
	if err != nil || release == nil {
		t.Fatalf("media response = release:%v err:%v", release != nil, err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := admission.admit(waitCtx, 512); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("buffer reservation released before response finished: %v", err)
	}
	release()
	secondRelease, err := admission.admit(context.Background(), 512)
	if err != nil {
		t.Fatalf("reservation was not released after response completion: %v", err)
	}
	secondRelease()
}
