package schema

import _ "embed"

//go:embed core-memory.v0.1.json
var CoreMemoryV01 []byte

const CoreMemoryV01URL = "https://ltm-cli.dev/schema/core-memory.v0.1.json"
