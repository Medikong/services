export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME !== "nodejs") {
    return;
  }
  const { getConfig, validateRuntimeConfig } = await import("@/server/bff/config");
  validateRuntimeConfig();
  const config = getConfig();
  console.log(
    JSON.stringify({
      timestamp: new Date().toISOString(),
      level: "info",
      message: "dropmong-web runtime initialized",
      service: "dropmong-web",
      version: config.appVersion,
      environment: config.appEnvironment,
      development_mocks: config.developmentMocks,
    }),
  );
}
