package application

import "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"

func Register(r *handler.Registry) {
	r.Register("validate-application", validateApplication)
	r.Register("credit-score", creditScore)
	r.Register("fraud-screen", fraudScreen)
	r.Register("escalate-review", escalateReview)
	r.Register("approve-loan", approveLoan)
}
