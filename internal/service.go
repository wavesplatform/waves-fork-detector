package internal

import "context"

type Service interface {
	Run(ctx context.Context)
	Shutdown()
}

type EmptyService struct{}

func (l *EmptyService) Run(_ context.Context) {}

func (l *EmptyService) Shutdown() {}
