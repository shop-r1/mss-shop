package memberlevels

import (
	"context"

	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
)

type Application interface {
	List(context.Context, ListOptions) (MemberLevelPage, error)
	Get(context.Context, string) (MemberLevel, error)
	Create(context.Context, CreateMemberLevelInput) (MemberLevel, error)
	Update(context.Context, string, UpdateMemberLevelInput) (MemberLevel, error)
	SetDefault(context.Context, string, RevisionInput) (MemberLevel, error)
	Delete(context.Context, string, RevisionInput) error
}

type requestApplication struct {
	database  business.RequestDatabase
	binding   fixedbinding.Binding
	mutations mutationGate
}

func (application *requestApplication) repository(ctx context.Context) (*repository, error) {
	if application == nil || application.database == nil || ctx == nil {
		return nil, ErrPersistence
	}
	database, available := application.database(ctx)
	if !available || database == nil {
		return nil, ErrPersistence
	}
	return newRepository(database, application.binding)
}

func (application *requestApplication) List(ctx context.Context, options ListOptions) (MemberLevelPage, error) {
	repository, err := application.repository(ctx)
	if err != nil {
		return MemberLevelPage{}, err
	}
	page, err := repository.List(ctx, options)
	if err != nil {
		return MemberLevelPage{}, err
	}
	page.Operations = application.mutations.availability
	return page, nil
}

func (application *requestApplication) Get(ctx context.Context, id string) (MemberLevel, error) {
	repository, err := application.repository(ctx)
	if err != nil {
		return MemberLevel{}, err
	}
	return repository.Get(ctx, id)
}

func (application *requestApplication) Create(ctx context.Context, input CreateMemberLevelInput) (MemberLevel, error) {
	if application == nil || !application.mutations.allows(PermissionCreate) {
		return MemberLevel{}, ErrMutationDisabled
	}
	values, err := input.values()
	if err != nil {
		return MemberLevel{}, err
	}
	repository, err := application.repository(ctx)
	if err != nil {
		return MemberLevel{}, err
	}
	return repository.Create(ctx, values)
}

func (application *requestApplication) Update(ctx context.Context, id string, input UpdateMemberLevelInput) (MemberLevel, error) {
	if application == nil || !application.mutations.allows(PermissionUpdate) {
		return MemberLevel{}, ErrMutationDisabled
	}
	values, err := input.values()
	if err != nil {
		return MemberLevel{}, err
	}
	repository, err := application.repository(ctx)
	if err != nil {
		return MemberLevel{}, err
	}
	return repository.Update(ctx, id, values)
}

func (application *requestApplication) SetDefault(ctx context.Context, id string, input RevisionInput) (MemberLevel, error) {
	if application == nil || !application.mutations.allows(PermissionSetDefault) {
		return MemberLevel{}, ErrMutationDisabled
	}
	revision, err := input.value()
	if err != nil {
		return MemberLevel{}, err
	}
	repository, err := application.repository(ctx)
	if err != nil {
		return MemberLevel{}, err
	}
	return repository.SetDefault(ctx, id, revision)
}

func (application *requestApplication) Delete(ctx context.Context, id string, input RevisionInput) error {
	if application == nil || !application.mutations.allows(PermissionDelete) {
		return ErrMutationDisabled
	}
	revision, err := input.value()
	if err != nil {
		return err
	}
	repository, err := application.repository(ctx)
	if err != nil {
		return err
	}
	return repository.Delete(ctx, id, revision)
}
