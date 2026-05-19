package disbursment

import (
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
)

func Register(r *handler.Registry) {
	r.Register("prepare-disbursement", prepareDisbursement)
	r.Register("transfer-funds", transferFunds)
	r.Register("notify-approval-overdue", notifyApprovalOverdue)
	r.Register("notify-disbursement", notifyDisbursement)

}
