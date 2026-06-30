import { pbkdf2, timingSafeEqual } from "node:crypto";
import { realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

import Fastify from "fastify";

const pbkdf2Async = promisify(pbkdf2);

const legacyPasswordScheme = "pbkdf2_sha256";
const benchmarkPasswordHash =
  "pbkdf2_sha256$210000$bWVkaWtvbmctYXV0aC1iZW5jaG1hcmstc2FsdA==$8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70=";
const benchmarkIterations = 210000;

export async function verifyLegacyPBKDF2(password, passwordHash) {
  const [scheme, iterationsRaw, saltB64, digestB64] = passwordHash.split("$");
  if (scheme !== legacyPasswordScheme || !iterationsRaw || !saltB64 || !digestB64) {
    throw new Error("unsupported password hash");
  }

  const iterations = Number.parseInt(iterationsRaw, 10);
  if (!Number.isInteger(iterations) || iterations < 1) {
    throw new Error("invalid iterations");
  }

  const salt = Buffer.from(saltB64, "base64");
  const expected = Buffer.from(digestB64, "base64");
  const actual = await pbkdf2Async(password, salt, iterations, expected.length, "sha256");
  return actual.length === expected.length && timingSafeEqual(actual, expected);
}

export function createBenchmarkServer() {
  const app = Fastify({ logger: false });

  app.get("/health", async () => ({ status: "ok" }));

  app.post("/bench/password/verify", async (request) => {
    const { password } = request.body ?? {};
    const verified = await verifyLegacyPBKDF2(password, benchmarkPasswordHash);
    return {
      verified,
      algorithm: legacyPasswordScheme,
      iterations: benchmarkIterations,
    };
  });

  return app;
}

if (import.meta.url === pathToFileURL(realpathSync(process.argv[1])).href) {
  const addr = process.argv.includes("--addr")
    ? process.argv[process.argv.indexOf("--addr") + 1]
    : "127.0.0.1:18681";
  const [host, portRaw] = addr.split(":");
  const port = Number.parseInt(portRaw, 10);
  const app = createBenchmarkServer();
  app.listen({ host, port }).then(() => {
    console.error(`fastify password benchmark server listening on ${addr}`);
  });
}
