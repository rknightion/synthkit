import { defineConfig } from "vitest/config";
import solid from "vite-plugin-solid";

export default defineConfig(({ command }) => ({
  plugins: [solid()],
  // One build works at any external prefix. The server inserts <base href>
  // before these relative assets; dev stays at / for its API-only proxy.
  base: command === "build" ? "./" : "/",
  server: {
    proxy: { "/control": "http://127.0.0.1:8088" }, // dev: forward API to a running synthkit
  },
  build: { outDir: "dist", emptyOutDir: false }, // false: never delete the tracked dist/.gitkeep
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["src/test/setup.ts"],
    // Restore spies (vi.spyOn) to their originals before each test. vitest v4 no
    // longer resets spy call history between tests, so without this a spy's
    // mock.calls leaks across tests in the same file (e.g. store.test.ts's
    // polling assertion saw 44 leaked getJSON calls from earlier tests).
    restoreMocks: true,
  },
}));
