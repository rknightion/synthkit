import { defineConfig } from "vitest/config";
import solid from "vite-plugin-solid";

export default defineConfig(({ command }) => ({
  plugins: [solid()],
  // build: served from the binary under /control/ui/. dev: served at / so the
  // /control proxy below only forwards API calls, not the app's own assets/HMR.
  base: command === "build" ? "/control/ui/" : "/",
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
