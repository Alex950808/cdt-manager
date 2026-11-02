package main

import (
	"testing"
	"time"
)

func TestParseHHMM(t *testing.T) {
	yes := map[string]int{"00:00": 0, "08:30": 510, "23:59": 1439, "24:00": 1440, " 09:05 ": 545, "8:3": 483}
	no := []string{"", "25:00", "12:60", "abc", "12", "-1:00", "24:30"}
	for s, want := range yes {
		if got, ok := parseHHMM(s); !ok || got != want {
			t.Errorf("parseHHMM(%q) = %d,%v want %d", s, got, ok, want)
		}
	}
	for _, s := range no {
		if _, ok := parseHHMM(s); ok {
			t.Errorf("parseHHMM(%q) should fail", s)
		}
	}
}

func TestDutyAtTimetable(t *testing.T) {
	// 甲 08:00-20:00，乙 20:00-00:00（到午夜）
	members := []GroupMember{
		{InstanceID: "s1", StartTime: "08:00", EndTime: "20:00"},
		{InstanceID: "s2", StartTime: "20:00", EndTime: "00:00"},
	}
	at := func(h, m int) time.Time {
		return time.Date(2025, 10, 15, h, m, 0, 0, time.FixedZone("CST", billTZOffset))
	}
	cases := []struct {
		h, m int
		want string
	}{
		{0, 0, "s1"},  // 00:00 属于空窗（乙到 23:59 为止）→ 兜底第一位
		{7, 59, "s1"}, // 空窗延续
		{8, 0, "s1"},  // 甲开始
		{12, 0, "s1"},
		{19, 59, "s1"},
		{20, 0, "s2"}, // 乙开始
		{23, 59, "s2"},
	}
	for _, c := range cases {
		if got := dutyAtTimetable(members, at(c.h, c.m)); got != c.want {
			t.Errorf("%02d:%02d duty = %q, want %q", c.h, c.m, got, c.want)
		}
	}
}

func TestDutyAtTimetableCrossMidnight(t *testing.T) {
	// 甲 22:00-06:00（跨天段），乙 06:00-22:00
	members := []GroupMember{
		{InstanceID: "s1", StartTime: "22:00", EndTime: "06:00"},
		{InstanceID: "s2", StartTime: "06:00", EndTime: "22:00"},
	}
	at := func(h, m int) time.Time {
		return time.Date(2025, 10, 15, h, m, 0, 0, time.FixedZone("CST", billTZOffset))
	}
	cases := []struct {
		h, m int
		want string
	}{
		{0, 0, "s1"}, // 跨天段前半夜之后的部分
		{5, 59, "s1"},
		{6, 0, "s2"},
		{21, 59, "s2"},
		{22, 0, "s1"}, // 跨天段开始
		{23, 30, "s1"},
	}
	for _, c := range cases {
		if got := dutyAtTimetable(members, at(c.h, c.m)); got != c.want {
			t.Errorf("%02d:%02d duty = %q, want %q", c.h, c.m, got, c.want)
		}
	}
}

func TestIsTimetableGroup(t *testing.T) {
	full := []GroupMember{
		{InstanceID: "s1", StartTime: "08:00", EndTime: "20:00"},
		{InstanceID: "s2", StartTime: "20:00", EndTime: "00:00"},
	}
	if !isTimetableGroup(full) {
		t.Error("full timetable should match")
	}
	partial := []GroupMember{
		{InstanceID: "s1", StartTime: "08:00", EndTime: "20:00"},
		{InstanceID: "s2"},
	}
	if isTimetableGroup(partial) {
		t.Error("partial timetable should not match")
	}
	bad := []GroupMember{
		{InstanceID: "s1", StartTime: "20:00", EndTime: "08:00"}, // 同日段 end<start 且非 0 → 无效
		{InstanceID: "s2", StartTime: "08:00", EndTime: "12:00"},
	}
	if isTimetableGroup(bad) {
		t.Error("invalid window should not match")
	}
}

func TestSlotForTimetable(t *testing.T) {
	store, eng := newTestEngine(t, &fakeCloud{states: map[string]*fakeState{}})
	g := Group{
		ID: "gt", Name: "时刻表组", Enabled: true, FailoverEnabled: true, Cycle: "day",
		Members: []GroupMember{
			{InstanceID: "s1", Position: 0, StartTime: "08:00", EndTime: "20:00"},
			{InstanceID: "s2", Position: 1, StartTime: "20:00", EndTime: "00:00"},
		},
	}
	_ = store.GroupSave(g)
	tz := time.FixedZone("CST", billTZOffset)
	slot := eng.slotFor(g, time.Date(2025, 10, 15, 9, 0, 0, 0, tz))
	if slot != "2025-10-15#480" {
		t.Errorf("slot = %q, want 2025-10-15#480", slot)
	}
}
