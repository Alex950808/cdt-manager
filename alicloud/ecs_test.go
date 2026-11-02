package alicloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDescribeInstanceExtractsFields(t *testing.T) {
	var gotQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = parseQueryOf(t, r)
		verifySignature(t, gotQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"RequestId":"r-1","TotalCount":1,
			"Instances":{"Instance":[{
				"InstanceId":"i-abc","Status":"Stopped","StoppedMode":"StopCharging",
				"RegionId":"cn-hongkong","ZoneId":"cn-hongkong-a",
				"EipAddress":{"AllocationId":"eip-1","IpAddress":"47.243.1.2"},
				"PublicIpAddress":{"IpAddress":["10.0.0.1"]}
			}]}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	inst, err := c.DescribeInstance(context.Background(), "cn-hongkong", "i-abc")
	if err != nil {
		t.Fatalf("DescribeInstance: %v", err)
	}
	if inst.Status != "Stopped" || inst.StoppedMode != "StopCharging" || inst.RegionID != "cn-hongkong" || inst.ZoneID != "cn-hongkong-a" {
		t.Errorf("instance fields wrong: %+v", inst)
	}
	if inst.PublicIP != "47.243.1.2" {
		t.Errorf("PublicIP = %q, want EIP 47.243.1.2", inst.PublicIP)
	}
	if gotQuery.Get("InstanceIds") != `["i-abc"]` || gotQuery.Get("RegionId") != "cn-hongkong" {
		t.Errorf("params wrong: InstanceIds=%q RegionId=%q", gotQuery.Get("InstanceIds"), gotQuery.Get("RegionId"))
	}
}

func TestDescribeInstancePrefersClassicIPWhenNoEIP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"RequestId":"r-1","Instances":{"Instance":[{
			"InstanceId":"i-x","Status":"Running",
			"EipAddress":{"IpAddress":""},
			"PublicIpAddress":{"IpAddress":["47.243.9.9","47.243.9.10"]}}]}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	inst, err := c.DescribeInstance(context.Background(), "cn-hongkong", "i-x")
	if err != nil {
		t.Fatalf("DescribeInstance: %v", err)
	}
	if inst.PublicIP != "47.243.9.9" {
		t.Errorf("PublicIP = %q, want first classic IP", inst.PublicIP)
	}
}

func TestDescribeInstanceMissing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"RequestId":"r-1","TotalCount":0,"Instances":{"Instance":[]}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	if _, err := c.DescribeInstance(context.Background(), "cn-hongkong", "i-none"); err == nil {
		t.Fatal("expected error for missing instance")
	}
}

func TestStartInstance(t *testing.T) {
	var gotInstanceID, gotAction string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := parseQueryOf(t, r)
		gotInstanceID = q.Get("InstanceId.1")
		gotAction = q.Get("Action")
		_, _ = w.Write([]byte(`{"RequestId":"r-2"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	if err := c.StartInstance(context.Background(), "cn-hongkong", "i-abc"); err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if gotInstanceID != "i-abc" {
		t.Errorf("InstanceId.1 = %q, want i-abc", gotInstanceID)
	}
	if gotAction != "StartInstances" {
		t.Errorf("Action = %q, want StartInstances", gotAction)
	}
}

func TestStartInstanceInsufficientInventoryIsTyped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"Code":"InsufficientInventory","Message":"No spot inventory in this region."}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	err := c.StartInstance(context.Background(), "cn-hongkong", "i-abc")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.IsInsufficientInventory() {
		t.Fatalf("want InsufficientInventory *APIError, got %T: %v", err, err)
	}
}

func TestStopInstanceModesAndFallback(t *testing.T) {
	t.Run("StopCharging accepted directly", func(t *testing.T) {
		var modes, actions, instanceIDs, forceStops []string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := parseQueryOf(t, r)
			modes = append(modes, q.Get("StoppedMode"))
			actions = append(actions, q.Get("Action"))
			instanceIDs = append(instanceIDs, q.Get("InstanceId.1"))
			forceStops = append(forceStops, q.Get("ForceStop"))
			_, _ = w.Write([]byte(`{"RequestId":"r-3"}`))
		}))
		defer ts.Close()

		c := newTestClient(t, ts)
		got, err := c.StopInstance(context.Background(), "cn-hongkong", "i-abc", StoppedModeStopCharging, true)
		if err != nil || got != StoppedModeStopCharging {
			t.Fatalf("got %q err=%v", got, err)
		}
		if len(modes) != 1 || modes[0] != StoppedModeStopCharging {
			t.Errorf("calls = %v", modes)
		}
		if len(actions) != 1 || actions[0] != "StopInstances" {
			t.Errorf("actions = %v", actions)
		}
		if len(instanceIDs) != 1 || instanceIDs[0] != "i-abc" {
			t.Errorf("instanceIDs = %v", instanceIDs)
		}
		if len(forceStops) != 1 || forceStops[0] != "true" {
			t.Errorf("forceStops = %v, want true", forceStops)
		}
	})

	t.Run("StopCharging rejected falls back to Stop", func(t *testing.T) {
		var modes, forceStops []string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := parseQueryOf(t, r)
			modes = append(modes, q.Get("StoppedMode"))
			forceStops = append(forceStops, q.Get("ForceStop"))
			if modes[len(modes)-1] == StoppedModeStopCharging {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"Code":"InvalidOperation.NotSupportedStoppedMode","Message":"Not supported."}`))
				return
			}
			_, _ = w.Write([]byte(`{"RequestId":"r-4"}`))
		}))
		defer ts.Close()

		c := newTestClient(t, ts)
		got, err := c.StopInstance(context.Background(), "cn-hongkong", "i-abc", StoppedModeStopCharging, true)
		if err != nil {
			t.Fatalf("fallback should succeed: %v", err)
		}
		if got != StoppedModeStop {
			t.Errorf("effective mode = %q, want Stop", got)
		}
		if len(modes) != 2 || modes[0] != StoppedModeStopCharging || modes[1] != StoppedModeKeepCharging {
			t.Errorf("call sequence = %v", modes)
		}
		for i, fs := range forceStops {
			if fs != "true" {
				t.Errorf("call %d ForceStop = %q, want true", i, fs)
			}
		}
	})

	t.Run("no fallback when disabled", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"Code":"InvalidOperation.NotSupportedStoppedMode","Message":"Not supported."}`))
		}))
		defer ts.Close()

		c := newTestClient(t, ts)
		if _, err := c.StopInstance(context.Background(), "cn-hongkong", "i-abc", StoppedModeStopCharging, false); err == nil {
			t.Fatal("expected error without fallback")
		}
	})
}

func TestJSONMustArray(t *testing.T) {
	if got := jsonMustArray("i-1"); got != `["i-1"]` {
		t.Errorf("jsonMustArray = %q", got)
	}
	var ids []string
	if err := json.Unmarshal([]byte(jsonMustArray("i-1")), &ids); err != nil || len(ids) != 1 || ids[0] != "i-1" {
		t.Errorf("output not a valid one-element JSON array: %v", err)
	}
}
