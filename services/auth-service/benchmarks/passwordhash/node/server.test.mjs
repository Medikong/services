import { once } from "node:events";
import test from "node:test";
import assert from "node:assert/strict";

import { createBenchmarkServer, verifyLegacyPBKDF2 } from "./server.mjs";

const fixturePassword = "benchmark-password-1234";
const fixtureWrongPassword = "wrong-password-1234";
const fixturePasswordHash =
  "pbkdf2_sha256$210000$bWVkaWtvbmctYXV0aC1iZW5jaG1hcmstc2FsdA==$8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70=";

test("PBKDF2 fixture matches the shared contract", async () => {
  assert.equal(await verifyLegacyPBKDF2(fixturePassword, fixturePasswordHash), true);
  assert.equal(await verifyLegacyPBKDF2(fixtureWrongPassword, fixturePasswordHash), false);
});

test("password verify API contract", async () => {
  const server = createBenchmarkServer().listen(0, "127.0.0.1");
  await once(server, "listening");
  try {
    const { port } = server.address();
    const response = await fetch(`http://127.0.0.1:${port}/bench/password/verify`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: fixturePassword }),
    });

    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), {
      verified: true,
      algorithm: "pbkdf2_sha256",
      iterations: 210000,
    });
  } finally {
    server.close();
  }
});
