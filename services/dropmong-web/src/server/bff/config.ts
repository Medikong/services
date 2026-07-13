import "server-only";

type Environment = Record<string, string | undefined>;

export type BffConfig = {
  appEnvironment: string;
  appOrigin: URL;
  appVersion: string;
  catalogBaseUrl: URL | null;
  developmentMocks: boolean;
  sellerContextBaseUrl: URL | null;
  sellerManagementBaseUrl: URL | null;
  sellerPortalEnabled: boolean;
  sellerScopeAudience: string | null;
  sellerScopeSigningKey: string | null;
  sessionCookieSecret: string;
  trustedIngressSecret: string | null;
};

const minimumSecretLength = 32;

export function getConfig(environment: Environment = process.env): BffConfig {
  const developmentMocks = environment.DEV_MOCK_MODE === "true";
  const appEnvironment = environment.APP_ENV ?? (developmentMocks ? "local" : "production");
  const originValue = environment.APP_ORIGIN ?? (developmentMocks ? "http://localhost:3000" : undefined);
  const sessionCookieSecret = environment.SESSION_COOKIE_SECRET;
  const sellerPortalEnabled = environment.SELLER_PORTAL_ENABLED === "true";

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
  const sellerContextBaseUrl = environment.SELLER_CONTEXT_INTERNAL_BASE_URL
    ? parseHttpUrl(environment.SELLER_CONTEXT_INTERNAL_BASE_URL, "SELLER_CONTEXT_INTERNAL_BASE_URL")
    : null;
  const sellerManagementBaseUrl = environment.SELLER_MANAGEMENT_INTERNAL_BASE_URL
    ? parseHttpUrl(environment.SELLER_MANAGEMENT_INTERNAL_BASE_URL, "SELLER_MANAGEMENT_INTERNAL_BASE_URL")
    : null;
  const sellerScopeSigningKey = environment.SELLER_SCOPE_SIGNING_KEY ?? null;
  const sellerScopeAudience = environment.SELLER_SCOPE_AUDIENCE ?? null;
  const trustedIngressSecret = environment.TRUSTED_INGRESS_SECRET ?? null;

  if (sellerPortalEnabled && !developmentMocks) {
    const missing = [
      ["SELLER_CONTEXT_INTERNAL_BASE_URL", sellerContextBaseUrl],
      ["SELLER_MANAGEMENT_INTERNAL_BASE_URL", sellerManagementBaseUrl],
      ["SELLER_SCOPE_SIGNING_KEY", sellerScopeSigningKey && sellerScopeSigningKey.length >= minimumSecretLength],
      ["SELLER_SCOPE_AUDIENCE", sellerScopeAudience],
      ["TRUSTED_INGRESS_SECRET", trustedIngressSecret && trustedIngressSecret.length >= minimumSecretLength],
    ].filter(([, value]) => !value).map(([name]) => name);
    if (missing.length > 0) {
      throw new Error(`Seller portal is enabled but required settings are missing: ${missing.join(", ")}.`);
    }
  }

  return {
    appEnvironment,
    appOrigin,
    appVersion: environment.APP_VERSION ?? "0.1.0",
    catalogBaseUrl,
    developmentMocks,
    sellerContextBaseUrl,
    sellerManagementBaseUrl,
    sellerPortalEnabled,
    sellerScopeAudience,
    sellerScopeSigningKey,
    sessionCookieSecret,
    trustedIngressSecret,
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
