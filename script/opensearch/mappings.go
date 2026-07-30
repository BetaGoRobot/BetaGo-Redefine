package opensearchschema

import _ "embed"

//go:embed agent_conversation_events_v1.json
var ConversationEventsV1 []byte

//go:embed agent_conversation_evaluations_v1.json
var ConversationEvaluationsV1 []byte
