package types

import "time"

const (
	HTTPTimeout            = 30 * time.Second
	HTTPShutdownTimeout    = 3 * time.Second
	TLSALPN01ChallengeType = "TLS-ALPN-01"
	HTTP01ChallengeType    = "HTTP-01"

	JSONLogFormat = "json"
	TextLogFormat = "text"

	MaxHostnameLength = 255
)

// ElectionMode describes how the coordinator is elected.
type ElectionMode string

const (
	ElectionModeAuto    ElectionMode = "auto"   // any node may become coordinator
	ElectionModeManual  ElectionMode = "manual" // fixed primary set by the operator
	ElectionModeUnknown ElectionMode = ""
)
