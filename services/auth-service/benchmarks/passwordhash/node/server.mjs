import { createServer } from "node:http";
import { pbkdf2 } from "node:crypto";
import { timingSafeEqual } from "node:crypto";
import { realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

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
  return createServer(async (request, response) => {
    try {
      if (request.method === "GET" && request.url === "/health") {
        writeJSON(response, 200, { status: "ok" });
        return;
      }

      if (request.method === "POST" && request.url === "/bench/password/verify") {
        const body = await readBody(request);
        const parsed = JSON.parse(body);
        const verified = await verifyLegacyPBKDF2(parsed.password, benchmarkPasswordHash);
        writeJSON(response, 200, {
          verified,
          algorithm: legacyPasswordScheme,
          iterations: benchmarkIterations,
        });
        return;
      }

      writeJSON(response, 404, { error: "not found" });
    } catch {
      writeJSON(response, 400, { error: "invalid request" });
    }
  });
}

function readBody(request) {
  return new Promise((resolve, reject) => {
    let body = "";
    request.setEncoding("utf8");
    request.on("data", (chunk) => {
      body += chunk;
    });
    request.on("end", () => resolve(body));
    request.on("error", reject);
  });
}

function writeJSON(response, status, value) {
  response.writeHead(status, { "Content-Type": "application/json" });
  response.end(`${JSON.stringify(value)}\n`);
}

if (import.meta.url === pathToFileURL(realpathSync(process.argv[1])).href) {
  const addr = process.argv.includes("--addr")
    ? process.argv[process.argv.indexOf("--addr") + 1]
    : "127.0.0.1:18481";
  const [host, portRaw] = addr.split(":");
  const port = Number.parseInt(portRaw, 10);
  createBenchmarkServer().listen(port, host, () => {
    console.error(`node password benchmark server listening on ${addr}`);
  });
}
