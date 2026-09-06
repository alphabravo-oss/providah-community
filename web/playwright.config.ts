import { defineConfig } from "@playwright/test";
import { readFileSync } from "node:fs";
const env = Object.fromEntries(
  readFileSync("../.env", "utf8")
    .trim()
    .split("\n")
    .map((line) => {
      const i = line.indexOf("=");
      return [line.slice(0, i), line.slice(i + 1)];
    }),
);
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: "http://localhost:5174",
    viewport: { width: 1440, height: 1000 },
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "go run ./cmd/providah",
      cwd: "..",
      url: "http://127.0.0.1:8081/readyz",
      env: {
        ...env,
        AUTOMATION_RUNTIMES: JSON.stringify([{runtime:"opentofu",image:"sha256:"+"a".repeat(64),version:"test"}]),
        DATABASE_URL:
          "postgres://providah:providah@127.0.0.1:55432/providah_browser?sslmode=disable",
        ORIGIN: "http://localhost:5174",
        LISTEN_ADDR: "127.0.0.1:8081",
      },
      timeout: 60000,
    },
    {
      command: "npm run dev -- --port 5174",
      url: "http://localhost:5174",
      env: { API_PROXY: "http://127.0.0.1:8081" },
      timeout: 60000,
    },
  ],
});
