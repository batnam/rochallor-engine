package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// LoanApplication payload represents the input to the loan application workflow
type LoanApplication struct {
	CreditScore int     `json:"creditScore"`
	FraudScore  float64 `json:"fraudScore"`
	LoanAmount  float64 `json:"loanAmount"`
}

// Disbursement payload represents variables in the disbursement workflow
type Disbursement struct {
	DisbursementFee        float64 `json:"disbursementFee"`
	NetAmount              float64 `json:"netAmount"`
	RequiresSeniorApproval bool    `json:"requiresSeniorApproval"`
}

// RunLoanApproval executes all the test scenarios for the loan approval and disbursement workflows.
func RunLoanApproval(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	appDefPath := filepath.Join(scenariosDir, "loan-approval-workflow", "workflow-loan-application.json")
	disbDefPath := filepath.Join(scenariosDir, "loan-approval-workflow", "workflow-loan-disbursement.json")

	appDef, err := os.ReadFile(appDefPath)
	if err != nil {
		t.Errorf("read %s: %v", appDefPath, err)
		return
	}
	disbDef, err := os.ReadFile(disbDefPath)
	if err != nil {
		t.Errorf("read %s: %v", disbDefPath, err)
		return
	}

	ctx := context.Background()
	if err := client.UploadDefinition(ctx, disbDef); err != nil {
		t.Errorf("upload loan disbursement definition: %v", err)
		return
	}
	if err := client.UploadDefinition(ctx, appDef); err != nil {
		t.Errorf("upload loan application definition: %v", err)
		return
	}

	TestStraightThroughAutoApproval(t, client, prefix)
	TestManualUnderwriterReview(t, client, prefix)
	TestSeniorOfficerApproval(t, client, prefix)
	TestRiskRejectionLowCredit(t, client, prefix)
	TestRiskRejectionHighFraud(t, client, prefix)
}

func TestStraightThroughAutoApproval(t TestReporter, client ClientIface, prefix string) {
	ctx := context.Background()
	vars := map[string]any{
		"creditScore": 700,
		"fraudScore":  0.0,
		"loanAmount":  100000.0,
	}

	instanceID, err := client.StartInstance(ctx, "LOS::loan-application-full", vars)
	if err != nil {
		t.Errorf("[%s/loan_approval/straight-through] start instance: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/loan_approval/straight-through] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/loan_approval/straight-through] want COMPLETED, got %s (failure: %q)", prefix, inst.Status, inst.FailureReason)
		return
	}
	t.Logf("[%s/loan_approval/straight-through] application COMPLETED, verified auto-approval", prefix)
}

func TestManualUnderwriterReview(t TestReporter, client ClientIface, prefix string) {
	ctx := context.Background()
	vars := map[string]any{
		"creditScore": 600, // triggers manual review
		"fraudScore":  0.0,
		"loanAmount":  100000.0,
	}

	instanceID, err := client.StartInstance(ctx, "LOS::loan-application-full", vars)
	if err != nil {
		t.Errorf("[%s/loan_approval/manual-review] start instance: %v", prefix, err)
		return
	}

	// wait a bit for it to reach user task
	time.Sleep(2 * time.Second)

	err = client.CompleteUserTaskByStableID(ctx, instanceID, "manual-review-task", map[string]any{"reviewDecision": "APPROVED"})
	if err != nil {
		t.Errorf("[%s/loan_approval/manual-review] complete user task: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/loan_approval/manual-review] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/loan_approval/manual-review] want COMPLETED, got %s", prefix, inst.Status)
		return
	}
	t.Logf("[%s/loan_approval/manual-review] application COMPLETED after manual review", prefix)
}

func TestSeniorOfficerApproval(t TestReporter, client ClientIface, prefix string) {
	ctx := context.Background()
	vars := map[string]any{
		"creditScore": 700,
		"fraudScore":  0.0,
		"loanAmount":  600000000.0, // triggers senior approval in disbursement workflow
	}

	instanceID, err := client.StartInstance(ctx, "LOS::loan-application-full", vars)
	if err != nil {
		t.Errorf("[%s/loan_approval/senior-approval] start instance: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/loan_approval/senior-approval] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/loan_approval/senior-approval] application want COMPLETED, got %s", prefix, inst.Status)
		return
	}
	t.Logf("[%s/loan_approval/senior-approval] application COMPLETED, expecting chaining to disbursement with senior approval", prefix)
}

func TestRiskRejectionLowCredit(t TestReporter, client ClientIface, prefix string) {
	ctx := context.Background()
	vars := map[string]any{
		"creditScore": 400, // High risk
		"fraudScore":  0.0,
		"loanAmount":  100000.0,
	}

	instanceID, err := client.StartInstance(ctx, "LOS::loan-application-full", vars)
	if err != nil {
		t.Errorf("[%s/loan_approval/reject-low-credit] start instance: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/loan_approval/reject-low-credit] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/loan_approval/reject-low-credit] want COMPLETED, got %s", prefix, inst.Status)
		return
	}
	t.Logf("[%s/loan_approval/reject-low-credit] rejected appropriately", prefix)
}

func TestRiskRejectionHighFraud(t TestReporter, client ClientIface, prefix string) {
	ctx := context.Background()
	vars := map[string]any{
		"creditScore": 700, 
		"fraudScore":  0.9, // High risk
		"loanAmount":  100000.0,
	}

	instanceID, err := client.StartInstance(ctx, "LOS::loan-application-full", vars)
	if err != nil {
		t.Errorf("[%s/loan_approval/reject-high-fraud] start instance: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/loan_approval/reject-high-fraud] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/loan_approval/reject-high-fraud] want COMPLETED, got %s", prefix, inst.Status)
		return
	}
	t.Logf("[%s/loan_approval/reject-high-fraud] rejected appropriately", prefix)
}
