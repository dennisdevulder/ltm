package schema

import _ "embed"

//go:embed core-memory.v0.1.json
var CoreMemoryV01 []byte

const CoreMemoryV01URL = "https://ltm-cli.dev/schema/core-memory.v0.1.json"

//go:embed core-memory.v0.2.json
var CoreMemoryV02 []byte

const CoreMemoryV02URL = "https://ltm-cli.dev/schema/core-memory.v0.2.json"

// Current is the version 'ltm write' and New() produce by default.
const Current = "0.2"
