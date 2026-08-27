package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/shop-r1/mss-shop/internal/platform/jobs"
	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

var (
	ErrDuplicateHandler = errors.New("duplicate job handler")
	ErrUnknownHandler   = errors.New("unknown job handler")
)

type Scope struct {
	tenantID tenancy.TenantID
}

func (s Scope) TenantID() tenancy.TenantID {
	return s.tenantID
}

type Handler interface {
	Kind() string
	Version() int
	Handle(context.Context, Scope, json.RawMessage) error
}

type Result struct {
	Handled   bool
	Duplicate bool
	Requeue   bool
}

type handlerKey struct {
	kind    string
	version int
}

type Dispatcher struct {
	inbox    Inbox
	handlers map[handlerKey]Handler
}

func New(inbox Inbox, handlers ...Handler) (*Dispatcher, error) {
	if inbox == nil {
		return nil, errors.New("inbox is required")
	}
	dispatcher := &Dispatcher{
		inbox:    inbox,
		handlers: make(map[handlerKey]Handler, len(handlers)),
	}
	for _, handler := range handlers {
		if handler == nil || handler.Kind() == "" || handler.Version() < 1 {
			return nil, errors.New("handler kind and positive version are required")
		}
		key := handlerKey{kind: handler.Kind(), version: handler.Version()}
		if _, duplicate := dispatcher.handlers[key]; duplicate {
			return nil, fmt.Errorf("%w: %s v%d", ErrDuplicateHandler, key.kind, key.version)
		}
		dispatcher.handlers[key] = handler
	}
	return dispatcher, nil
}

func (d *Dispatcher) Dispatch(
	ctx context.Context,
	envelope jobs.Envelope,
) (Result, error) {
	if err := envelope.Validate(); err != nil {
		return Result{}, err
	}
	handler, exists := d.handlers[handlerKey{kind: envelope.Kind, version: envelope.Version}]
	if !exists {
		return Result{}, fmt.Errorf(
			"%w: %s v%d",
			ErrUnknownHandler,
			envelope.Kind,
			envelope.Version,
		)
	}

	claim, err := d.inbox.Begin(ctx, envelope.TenantID, envelope.ID)
	if err != nil {
		return Result{Requeue: errors.Is(err, ErrInProgress)}, err
	}
	if claim.Duplicate() {
		return Result{Duplicate: true}, nil
	}

	scope := Scope{tenantID: envelope.TenantID}
	if err := handler.Handle(ctx, scope, append(json.RawMessage(nil), envelope.Payload...)); err != nil {
		if releaseErr := d.inbox.Release(context.WithoutCancel(ctx), claim); releaseErr != nil {
			return Result{Requeue: true}, errors.Join(err, releaseErr)
		}
		return Result{Requeue: true}, err
	}
	if err := d.inbox.Complete(context.WithoutCancel(ctx), claim); err != nil {
		return Result{Requeue: true}, err
	}
	return Result{Handled: true}, nil
}
