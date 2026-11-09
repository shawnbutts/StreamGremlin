package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRewardsThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	list := func() RewardList {
		_, out := call("GET", "/v1/rewards", "")
		var l RewardList
		_ = json.Unmarshal([]byte(out), &l)
		return l
	}
	if l := list(); l.State != RewardsNotSignedIn || len(l.Rewards) != 0 {
		t.Fatalf("signed out: %+v", l)
	}
	if code, _ := call("POST", "/v1/rewards", `{"title":"x","cost":1}`); code != 409 {
		t.Fatal("making a reward signed out:", code)
	}
	call("PUT", "/v1/connections/twitch/client-id", `{"client_id":"abcdefghij0123456789"}`)
	_, out := call("POST", "/v1/connections/twitch/streamer/sign-in", "")
	var p struct {
		UserCode string `json:"user_code"`
	}
	_ = json.Unmarshal([]byte(out), &p)
	_ = testMock.Approve(p.UserCode, "rewardstreamer")
	deadline := time.Now().Add(5 * time.Second)
	for list().State != RewardsOK {
		if time.Now().After(deadline) {
			t.Fatalf("never listed: %+v", list())
		}
		time.Sleep(50 * time.Millisecond)
	}
	testMock.AddDashboardReward("rewardstreamer", "Hydrate", 100)

	code, out := call("POST", "/v1/rewards", `{"title":"Group hug","cost":500,"prompt":"Who?","input_required":true}`)
	var hug struct{ ID string }
	_ = json.Unmarshal([]byte(out), &hug)
	if code != 201 || !strings.Contains(out, `"managed":true`) {
		t.Fatalf("make: %d %s", code, out)
	}
	if code, _ := call("POST", "/v1/rewards", `{"title":"Group hug","cost":500}`); code != 200 {
		t.Fatal("making it again:", code)
	}
	if code, out := call("POST", "/v1/rewards", `{"title":"Hydrate","cost":5}`); code != 400 || !strings.Contains(out, "has that title") {
		t.Fatalf("a taken title: %d %s", code, out)
	}
	l := list()
	if !l.CanManage || len(l.Rewards) != 2 || l.Max != 50 || l.Rewards[1].Managed {
		t.Fatalf("%+v", l)
	}
	if code, out := call("PUT", "/v1/rewards/"+l.Rewards[1].ID, `{"paused":true}`); code != 403 || !strings.Contains(out, "made in StreamGremlin") {
		t.Fatalf("change a Twitch-made reward: %d %s", code, out)
	}
	if code, out := call("PUT", "/v1/rewards/"+hug.ID, `{"cost":750,"paused":true}`); code != 200 || !strings.Contains(out, `"cost":750`) {
		t.Fatalf("change: %d %s", code, out)
	}

	id := testMock.Redeem("rewardstreamer", "Fan", "Group hug", 0, "everyone")
	if code, out := call("GET", "/v1/rewards/"+hug.ID+"/redemptions", ""); code != 200 || !strings.Contains(out, `"input":"everyone"`) {
		t.Fatalf("queue: %d %s", code, out)
	}
	if code, out := call("POST", "/v1/rewards/"+hug.ID+"/redemptions/"+id, `{"action":"refund"}`); code != 200 || !strings.Contains(out, `"status":"canceled"`) {
		t.Fatalf("refund: %d %s", code, out)
	}
	if code, out := call("POST", "/v1/rewards/"+hug.ID+"/redemptions/"+id, `{"action":"refund"}`); code != 200 || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("refund again: %d %s", code, out)
	}
	if code, _ := call("POST", "/v1/rewards/"+hug.ID+"/redemptions/"+id, `{"action":"done"}`); code != 409 {
		t.Fatal("done after a refund:", code)
	}
	if code, _ := call("POST", "/v1/rewards/"+hug.ID+"/redemptions/"+id, `{"action":"maybe"}`); code != 400 {
		t.Fatal(code)
	}
	if code, out := call("DELETE", "/v1/rewards/"+hug.ID, ""); code != 200 || !strings.Contains(out, `"deleted":true`) {
		t.Fatalf("delete: %d %s", code, out)
	}

	testMock.SetAffiliate("rewardstreamer", false)
	if l := list(); l.State != RewardsNoChannelPoint || !strings.Contains(l.Detail, "Affiliate or Partner") {
		t.Fatalf("not affiliate: %+v", l)
	}
}
