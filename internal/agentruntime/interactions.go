package agentruntime

import (
	"context"
	"sync"

	"github.com/topcheer/ggcode/internal/permission"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

type InteractionBroker struct {
	mu        sync.Mutex
	approvals map[string]approvalWaiter
	askUsers  map[string]askUserWaiter
}

type approvalWaiter struct {
	request ApprovalRequest
	resp    chan permission.Decision
}

type askUserWaiter struct {
	request AskUserRequest
	resp    chan toolpkg.AskUserResponse
}

func NewInteractionBroker() *InteractionBroker {
	return &InteractionBroker{
		approvals: make(map[string]approvalWaiter),
		askUsers:  make(map[string]askUserWaiter),
	}
}

func (b *InteractionBroker) AwaitApproval(ctx context.Context, req ApprovalRequest) permission.Decision {
	ch := make(chan permission.Decision, 1)
	b.mu.Lock()
	b.approvals[req.ID] = approvalWaiter{request: req, resp: ch}
	b.mu.Unlock()

	select {
	case decision := <-ch:
		return decision
	case <-ctx.Done():
		b.removeApproval(req.ID)
		return permission.Deny
	}
}

func (b *InteractionBroker) AwaitAskUser(ctx context.Context, req AskUserRequest) (toolpkg.AskUserResponse, error) {
	ch := make(chan toolpkg.AskUserResponse, 1)
	b.mu.Lock()
	b.askUsers[req.ID] = askUserWaiter{request: req, resp: ch}
	b.mu.Unlock()

	select {
	case response := <-ch:
		return response, nil
	case <-ctx.Done():
		b.removeAskUser(req.ID)
		return CancelledAskUserResponse(req.Request), ctx.Err()
	}
}

func (b *InteractionBroker) ResolveApproval(id string, decision permission.Decision) (ApprovalRequest, bool) {
	waiter, ok := b.removeApproval(id)
	if !ok {
		return ApprovalRequest{}, false
	}
	select {
	case waiter.resp <- decision:
	default:
	}
	return waiter.request, true
}

func (b *InteractionBroker) ResolveAskUser(id string, response toolpkg.AskUserResponse) (AskUserRequest, bool) {
	waiter, ok := b.removeAskUser(id)
	if !ok {
		return AskUserRequest{}, false
	}
	select {
	case waiter.resp <- response:
	default:
	}
	return waiter.request, true
}

func (b *InteractionBroker) PendingApproval(id string) (ApprovalRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	waiter, ok := b.approvals[id]
	if !ok {
		return ApprovalRequest{}, false
	}
	return waiter.request, true
}

func (b *InteractionBroker) PendingAskUser(id string) (AskUserRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	waiter, ok := b.askUsers[id]
	if !ok {
		return AskUserRequest{}, false
	}
	return waiter.request, true
}

func (b *InteractionBroker) FirstPendingApproval() (ApprovalRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, waiter := range b.approvals {
		return waiter.request, true
	}
	return ApprovalRequest{}, false
}

func (b *InteractionBroker) FirstPendingAskUser() (AskUserRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, waiter := range b.askUsers {
		return waiter.request, true
	}
	return AskUserRequest{}, false
}

func (b *InteractionBroker) ApprovalCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.approvals)
}

func (b *InteractionBroker) AskUserCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.askUsers)
}

func (b *InteractionBroker) CancelAll() ([]ApprovalRequest, []AskUserRequest) {
	b.mu.Lock()
	approvals := make([]approvalWaiter, 0, len(b.approvals))
	for _, waiter := range b.approvals {
		approvals = append(approvals, waiter)
	}
	askUsers := make([]askUserWaiter, 0, len(b.askUsers))
	for _, waiter := range b.askUsers {
		askUsers = append(askUsers, waiter)
	}
	b.approvals = make(map[string]approvalWaiter)
	b.askUsers = make(map[string]askUserWaiter)
	b.mu.Unlock()

	approvalRequests := make([]ApprovalRequest, 0, len(approvals))
	for _, waiter := range approvals {
		approvalRequests = append(approvalRequests, waiter.request)
		select {
		case waiter.resp <- permission.Deny:
		default:
		}
	}

	askUserRequests := make([]AskUserRequest, 0, len(askUsers))
	for _, waiter := range askUsers {
		askUserRequests = append(askUserRequests, waiter.request)
		select {
		case waiter.resp <- CancelledAskUserResponse(waiter.request.Request):
		default:
		}
	}

	return approvalRequests, askUserRequests
}

func CancelledAskUserResponse(req toolpkg.AskUserRequest) toolpkg.AskUserResponse {
	return toolpkg.AskUserResponse{
		Status:        toolpkg.AskUserStatusCancelled,
		Title:         req.Title,
		QuestionCount: len(req.Questions),
	}
}

func (b *InteractionBroker) removeApproval(id string) (approvalWaiter, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	waiter, ok := b.approvals[id]
	if ok {
		delete(b.approvals, id)
	}
	return waiter, ok
}

func (b *InteractionBroker) removeAskUser(id string) (askUserWaiter, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	waiter, ok := b.askUsers[id]
	if ok {
		delete(b.askUsers, id)
	}
	return waiter, ok
}
