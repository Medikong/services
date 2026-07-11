import "server-only";

type Environment = Record<string, string | undefined>;

export type BffConfig = {
  appEnvironment: string;
  appOrigin: URL;
  appVersion: string;
  catalogBaseUrl: URL | null;
  developmentMocks: boolean;
  sessionCookieSecret: string;
};

const minimumSecretLength = 32;

export function getConfig(environment: Environment = process.env): BffConfig {
  const developmentMocks = environment.DEV_MOCK_MODE === "true";
  const appEnvironment = environment.APP_ENV ?? (developmentMocks ? "local" : "production");
  const originValue = environment.APP_ORIGIN ?? (developmentMocks ? "http://localhost:3000" : undefined);
  const sessionCookieSecret = environment.SESSION_COOKIE_SECRET;

  if (!originValue) {
    throw new Error("APP_ORIGIN is required when DEV_MOCK_MODE is not enabled.");
  }
  if (!sessionCookieSecret || sessionCookieSecret.length < minimumSecretLength) {
    throw new Error(`SESSION_COOKIE_SECRET must be at least ${minimumSecretLength} characters long.`);
  }

  const appOrigin = parseHttpUrl(originValue, "APP_ORIGIN");
  const catalogBaseUrl = environment.CATALOG_INTERNAL_BASE_URL
    ? parseHttpUrl(environment.CATALOG_INTERNAL_BASE_URL, "CATALOG_INTERNAL_BASE_URL")
    : null;

  return {
    appEnvironment,
    appOrigin,
    appVersion: environment.APP_VERSION ?? "0.1.0",
    catalogBaseUrl,
    developmentMocks,
    sessionCookieSecret,
  };
}

export function validateRuntimeConfig(): void {
  getConfig();
}

function parseHttpUrl(value: string, variableName: string): URL {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error(`${variableName} must be a valid absolute URL.`);
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error(`${variableName} must use http or https.`);
  }
  return parsed;
}
