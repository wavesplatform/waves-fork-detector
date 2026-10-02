package main

import "context"

type Service interface {
	Run(ctx context.Context)
	Wait() error
	Shutdown()
}

type EmptyService struct{}

func (l *EmptyService) Run(_ context.Context) {}

func (l *EmptyService) Wait() error { return nil }

func (l *EmptyService) Shutdown() {}
