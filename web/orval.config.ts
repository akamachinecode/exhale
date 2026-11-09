import { defineConfig } from "orval";

// The REST contract is generated from the Go structs (huma). Regenerate with
// `mise run gen` (exports web/openapi.json, then runs orval).
export default defineConfig({
	exhale: {
		input: "./openapi.json",
		output: {
			target: "./src/gen/api.ts",
			client: "react-query",
			httpClient: "fetch",
			mode: "single",
			clean: true,
			override: {
				mutator: { path: "./src/lib/fetcher.ts", name: "fetcher" },
				query: { signal: true },
				fetch: { includeHttpResponseReturnType: false },
			},
		},
	},
});
