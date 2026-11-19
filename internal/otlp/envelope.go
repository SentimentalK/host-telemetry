package otlp

import "google.golang.org/protobuf/encoding/protowire"

func envelope(rm []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), rm)
}
