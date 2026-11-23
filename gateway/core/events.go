package core

import (
	"context"
	"net/http"
)

type ErrorEvent struct {
	Source       string
	Kind         string
	Reason       string
	Endpoint     string
	Provider     string
	ProviderCode string
	HTTPStatus   int
	Hint         string
	InputChars   int
	LatencyMs    int64
	DeviceHash   string

	LastOperation         string
	TerminationReason     string
	PhysFootprintMB       int64
	RSSMB                 int64
	SecondsSinceHeartbeat int64
	Signal                int64
	HangDurationMs        int64
	PeakMemoryMB          int64
	OSStatus              int64
	ModelID               string
	AliveCount            int64
	QwertyVisible         int64
	AppearFpMB            int64
	CrashStack            string
	AttributedTo          string
	ThreadSel             string
	ExceptionType         int64
	ExceptionCode         int64
	UptimeSeconds         int64
	FPHighWaterMB         int64
	HeadroomMB            int64
	ErrorDesc             string
	OSVersion             string
	AppBuild              string
}

var OnError func(ctx context.Context, e ErrorEvent)

var OnRequestFailed func(ctx context.Context, errorType string)

var OnEnhanceSkipped func(ctx context.Context, reason string)

func ReportEnhanceSkipped(ctx context.Context, reason string) {
	if OnEnhanceSkipped != nil {
		OnEnhanceSkipped(ctx, reason)
	}
}

var ResolveIntent func(ctx context.Context, r *http.Request, transcript, contextJSON, intent string) (string, bool)

func resolveIntent(ctx context.Context, r *http.Request, transcript, contextJSON, intent string) (string, bool) {
	if ResolveIntent == nil {
		return intent, false
	}
	return ResolveIntent(ctx, r, transcript, contextJSON, intent)
}
