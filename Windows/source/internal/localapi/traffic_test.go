// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"github.com/Irviodotnet/Irvio/internal/trafficstats"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"testing"
)

type trafficController struct{ fakeController }

func (*trafficController) TrafficStats() trafficstats.Snapshot {
	return trafficstats.Snapshot{SessionID: 9, ClientsValid: true, MyUpload: ^uint64(0), MyValid: true, MyDownload: 123, DownloadValid: true}
}

func TestTrafficStatsOptionalAndCompatibility(t *testing.T) {
	s := &Server{controller: &trafficController{}}
	request := &localv1.Request{Method: localv1.Method_METHOD_GET_TRAFFIC_STATS}
	response := s.handle(request)
	v := response.GetTrafficStats()
	if v == nil || v.SessionId != 9 || v.Clients == nil || v.MyUploadBytes == nil || *v.MyUploadBytes != ^uint64(0) || v.MyDownloadBytes == nil || v.GetMyDownloadBytes() != 123 || v.SharedUploadBytes != nil {
		t.Fatal(response)
	}
	if requiredMinor(request.Method) != 5 {
		t.Fatal("minor version gate")
	}
	s.controller = &fakeController{}
	if s.handle(request).Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED {
		t.Fatal("old runtime must report unsupported")
	}
}
