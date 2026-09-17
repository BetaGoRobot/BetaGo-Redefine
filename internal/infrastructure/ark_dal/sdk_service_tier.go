package ark_dal

import "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"

func init() {
	// SDK v1.2.51 has no flex constant. Its JSON encoder, decoder and String
	// method use these exported maps. Register once at package initialization
	// (before concurrent requests), including decoding flex responses/events.
	// Use a private negative value rather than guessing a future protobuf ID.
	// Remove this shim once the pinned SDK supplies flex natively.
	if _, supported := responses.ResponsesServiceTier_Enum_value["flex"]; !supported {
		const flex int32 = -1
		responses.ResponsesServiceTier_Enum_value["flex"] = flex
		responses.ResponsesServiceTier_Enum_name[flex] = "flex"
	}
}
