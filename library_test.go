package main

import (
	"strings"
	"testing"
	"time"

	"bardraw/internal/plan"
	"bardraw/internal/svg"
)

func TestComputePlanBudget(t *testing.T) {
	doc, _ := svg.Parse(strings.NewReader(`<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="45" fill="none" stroke="black"/></svg>`))
	g := plan.Prepare(doc, plan.PrepOptions{Mode: plan.ModeAuto})
	tm := plan.NewTiming(60, 0, 12, -1)
	full := ComputePlan(PlanReq{G: g, W: 800, H: 800, P: plan.Params{Level: 7}, Timing: tm})
	budget := full.ETA / 2
	auto := ComputePlan(PlanReq{G: g, W: 800, H: 800, P: plan.Params{Level: 7}, Timing: tm, Budget: budget, AutoLower: true})
	if !auto.AutoReduced || auto.ETA > budget || auto.OverBudget() {
		t.Fatalf("auto: lvl %d eta %v budget %v", auto.Plan.Level, auto.ETA, budget)
	}
	off := ComputePlan(PlanReq{G: g, W: 800, H: 800, P: plan.Params{Level: 7}, Timing: tm, Budget: budget})
	if off.AutoReduced || off.Plan.Level != 7 || !off.OverBudget() {
		t.Fatalf("off: lvl %d over %v", off.Plan.Level, off.OverBudget())
	}
	if none := ComputePlan(PlanReq{G: g, W: 800, H: 800, P: plan.Params{Level: 7}, Timing: tm, AutoLower: true}); none.OverBudget() || none.AutoReduced {
		t.Fatal("no budget must never be over budget")
	}
	_ = time.Second
}
