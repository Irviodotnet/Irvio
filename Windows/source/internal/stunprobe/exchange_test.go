// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestChangeRequestEncoding(t *testing.T) {
	tests := []struct {
		name       string
		changeIP   bool
		changePort bool
		want       []byte
	}{
		{name: "none"},
		{name: "port", changePort: true, want: []byte{0, 0, 0, 0x02}},
		{name: "IP and port", changeIP: true, changePort: true, want: []byte{0, 0, 0, 0x06}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
			addChangeRequest(message, test.changeIP, test.changePort)
			value, err := message.Get(stun.AttrChangeRequest)
			if test.want == nil {
				if !errors.Is(err, stun.ErrAttributeNotFound) {
					t.Fatalf("unexpected CHANGE-REQUEST: %x, %v", value, err)
				}
				return
			}
			if err != nil || string(value) != string(test.want) {
				t.Fatalf("CHANGE-REQUEST = %x, %v; want %x", value, err, test.want)
			}
		})
	}
}

func TestAlternateResponseSourcePolicySeparatesSuccessAndError(t *testing.T) {
	registry, err := NewRegistry(testRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	classifier := NewClassifier(registry)
	primary := netip.MustParseAddrPort("127.0.0.1:3478")
	alternate := netip.MustParseAddrPort("127.0.0.2:3479")
	policy := responseSourcePolicy{
		success: alternate, errors: [MaxResponseSources]netip.AddrPort{primary}, errorCount: 1,
	}
	id := transactionID{4, 5, 6}
	if _, err := registry.register(id, policy, socketmux.IPv4, time.Now()); err != nil {
		t.Fatal(err)
	}
	success, err := buildResponse(id, primary, false, false)
	if err != nil {
		t.Fatal(err)
	}
	errorResponse, err := buildResponse(id, primary, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := classifier.Classify(success.Raw, primary, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("success from request destination = %v, want Data", got)
	}
	if got := classifier.Classify(success.Raw, alternate, socketmux.IPv4); got != socketmux.Control {
		t.Fatalf("success from alternate = %v, want Control", got)
	}
	if got := classifier.Classify(errorResponse.Raw, alternate, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("error from alternate = %v, want Data", got)
	}
	if got := classifier.Classify(errorResponse.Raw, primary, socketmux.IPv4); got != socketmux.Control {
		t.Fatalf("error from primary = %v, want Control", got)
	}
}

func TestBindingRequestSourcePolicyValidationIsBounded(t *testing.T) {
	primary := netip.MustParseAddrPort("127.0.0.1:3478")
	alternate := netip.MustParseAddrPort("127.0.0.2:3479")
	request := BindingRequest{Destination: primary, ExpectedSuccessSource: alternate}
	normalized, policy, family, err := normalizeBindingRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if family != socketmux.IPv4 || len(normalized.AllowedErrorSources) != 1 || normalized.AllowedErrorSources[0] != primary || !policy.matches(primary, stun.ClassErrorResponse) {
		t.Fatalf("unexpected normalized policy: %+v %+v %v", normalized, policy, family)
	}

	tooMany := request
	for port := uint16(4000); port < 4000+MaxResponseSources+1; port++ {
		tooMany.AllowedErrorSources = append(tooMany.AllowedErrorSources, netip.AddrPortFrom(primary.Addr(), port))
	}
	if _, _, _, err := normalizeBindingRequest(tooMany); !errors.Is(err, ErrInvalidSourcePolicy) {
		t.Fatalf("oversized policy error = %v", err)
	}
	mixed := request
	mixed.AllowedErrorSources = []netip.AddrPort{netip.MustParseAddrPort("[::1]:3478")}
	if _, _, _, err := normalizeBindingRequest(mixed); !errors.Is(err, ErrInvalidSourcePolicy) {
		t.Fatalf("mixed-family policy error = %v", err)
	}
}

func TestBindingResponseDecodesRFC5780AddressAttributes(t *testing.T) {
	client := &Client{}
	primary := netip.MustParseAddrPort("127.0.0.1:3478")
	other := netip.MustParseAddrPort("127.0.0.2:3479")
	mapped := netip.MustParseAddrPort("198.51.100.8:41000")
	id := transactionID{7, 8, 9}
	message := stun.MustBuild(stun.BindingSuccess)
	message.TransactionID = id
	message.WriteTransactionID()
	attributes := []stun.Setter{
		&stun.XORMappedAddress{IP: net.IP(mapped.Addr().AsSlice()), Port: int(mapped.Port())},
		&stun.MappedAddress{IP: net.IP(mapped.Addr().AsSlice()), Port: int(mapped.Port())},
		&stun.OtherAddress{IP: net.IP(other.Addr().AsSlice()), Port: int(other.Port())},
		&stun.ResponseOrigin{IP: net.IP(primary.Addr().AsSlice()), Port: int(primary.Port())},
	}
	for _, attribute := range attributes {
		if err := attribute.AddTo(message); err != nil {
			t.Fatal(err)
		}
	}
	request := BindingRequest{Destination: primary, ExpectedSuccessSource: primary, AllowedErrorSources: []netip.AddrPort{primary}}
	tx := &transaction{id: id, policy: testResponsePolicy(primary)}
	response, err := client.resultFromResponse(
		responseEvent{message: message, source: primary, family: socketmux.IPv4}, tx, request,
		netip.MustParseAddrPort("127.0.0.1:41000"), socketmux.IPv4, time.Now(), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.MappedEndpoint != mapped || !response.MappedAddress.Present || response.MappedAddress.AddrPort != mapped ||
		!response.OtherAddress.Present || response.OtherAddress.AddrPort != other ||
		!response.ResponseOrigin.Present || response.ResponseOrigin.AddrPort != primary {
		t.Fatalf("decoded response = %+v", response)
	}
}
