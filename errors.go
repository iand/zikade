package zikade

import "github.com/iand/xorbie/coordt"

// A ConfigurationError is returned when a component's configuration is found to be invalid or
// unusable. It is the type the coordinator and the state machines return, so an errors.As
// against it matches a configuration error raised anywhere in the DHT.
type ConfigurationError = coordt.ConfigurationError
