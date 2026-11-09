import babel from "@rolldown/plugin-babel";
import tailwindcss from "@tailwindcss/vite";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const api = process.env.EXHALE_API ?? "http://127.0.0.1:8484";

export default defineConfig({
	plugins: [react(), babel({ presets: [reactCompilerPreset()] }), tailwindcss()],
	resolve: { alias: { "@": new URL("./src", import.meta.url).pathname } },
	server: {
		port: 5173,
		proxy: { "/api": { target: api, changeOrigin: false }, "/mcp": api },
	},
	build: { outDir: "dist", emptyOutDir: false, sourcemap: false },
});
