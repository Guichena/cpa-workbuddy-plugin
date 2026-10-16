package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFetchCheckinStatus_ExposesShowCampaign 验证上游 showCampaign 被透传到摘要，
// 供面板区分「活动被服务端隐藏」与「账号不适用」两种无活动形态。
func TestFetchCheckinStatus_ExposesShowCampaign(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantShow bool
	}{
		{
			name:     "活动平台开启但列表无 CLAIM_BENEFIT（服务端隐藏）",
			body:     `{"showCampaign":true,"claimable":false,"campaigns":[{"campaignId":"v1","actionType":"VIEW_DETAILS","claimStatus":"CLAIMABLE"}]}`,
			wantShow: true,
		},
		{
			name:     "活动平台整体关闭（账号不适用）",
			body:     `{"showCampaign":false,"claimable":false,"campaigns":[]}`,
			wantShow: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			prev := campaignsURLForTest
			campaignsURLForTest = srv.URL
			t.Cleanup(func() { campaignsURLForTest = prev })

			sa := &storedAuth{}
			sa.Account.UID = "u-show-test"
			sa.Auth.AccessToken = "dt-test-token"

			sum, err := fetchCheckinStatus(sa)
			if err != nil {
				t.Fatalf("fetchCheckinStatus: %v", err)
			}
			if sum.ShowCampaign != tc.wantShow {
				t.Fatalf("ShowCampaign = %v, want %v", sum.ShowCampaign, tc.wantShow)
			}
			if sum.Active {
				t.Fatal("两种形态均无可领活动，Active 应为 false")
			}
		})
	}
}

// TestCheckinSummary_SerializesShowCampaign 验证 JSON 字段名为 show_campaign，
// 面板按此键名消费。
func TestCheckinSummary_SerializesShowCampaign(t *testing.T) {
	raw, err := json.Marshal(&checkinSummary{ShowCampaign: true, Active: false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, ok := m["show_campaign"]; !ok || got != true {
		t.Fatalf("JSON 应含 show_campaign=true，实际 %v", m)
	}
}

// TestCheckinMessage_DistinguishesHiddenFromIneligible 验证两种无活动形态产出
// 不同 message：show_campaign=true 为「未下发」，false 为「不适用」。
// 该分支位于 checkinOneAccount，通过构造 ci 直接断言其选词逻辑。
func TestCheckinMessage_DistinguishesHiddenFromIneligible(t *testing.T) {
	hidden := checkinNoActivityMessage(&checkinSummary{Active: false, ShowCampaign: true})
	ineligible := checkinNoActivityMessage(&checkinSummary{Active: false, ShowCampaign: false})
	if hidden == ineligible {
		t.Fatalf("两种形态的 message 不应相同，均为 %q", hidden)
	}
	if hidden != "签到活动暂未下发到该账号" {
		t.Fatalf("show_campaign=true 应提示未下发，实际 %q", hidden)
	}
	if ineligible != "该账号不适用签到活动" {
		t.Fatalf("show_campaign=false 应提示不适用，实际 %q", ineligible)
	}
}

// TestPickCheckinCampaign_UnaffectedByShowCampaign 验证 ShowCampaign 不参与
// 活动挑选（挑选只看 actionType/benefit/时间窗），避免新增字段改变既有语义。
func TestPickCheckinCampaign_UnaffectedByShowCampaign(t *testing.T) {
	now := time.Unix(1791700000, 0)
	q := &campaignStatusResponse{
		ShowCampaign: true,
		Campaigns: []campaign{{
			CampaignID:  "c1",
			ActionType:  "CLAIM_BENEFIT",
			ClaimStatus: "CLAIMABLE",
			StartAt:     1791684000,
			EndAt:       1791770340,
			Benefit:     benefit{Kind: "CREDITS", Amount: 100},
		}},
	}
	active, checked, cid, credit := pickCheckinCampaign(q, now)
	if !active || checked || cid != "c1" || credit != 100 {
		t.Fatalf("ShowCampaign 不应影响挑选结果，got active=%v checked=%v cid=%q credit=%d",
			active, checked, cid, credit)
	}
}
