import { config } from '@grafana/runtime';

type SandboxEligibilityCheckParams = {
  isAngular?: boolean;
  pluginId: string;
};

type SandboxEnabledCheck = (params: SandboxEligibilityCheckParams) => Promise<boolean>;

/**
 * We allow core extensions to register their own
 * sandbox enabled checks.
 */
const sandboxEnabledChecks: SandboxEnabledCheck[] = [isPluginFrontendSandboxEnabled];

export function addSandboxEnabledCheck(checker: SandboxEnabledCheck) {
  sandboxEnabledChecks.push(checker);
}

export async function shouldLoadPluginInFrontendSandbox({
  isAngular,
  pluginId,
}: SandboxEligibilityCheckParams): Promise<boolean> {
  // basic check if the plugin is eligible for the sandbox
  if (!isPluginFrontendSandboxElegible({ isAngular })) {
    return false;
  }

  for (const checker of sandboxEnabledChecks) {
    if (await checker({ isAngular, pluginId })) {
      return true;
    }
  }
  return false;
}

/**
 * This is a basic check that checks if the plugin is eligible to run in the sandbox.
 * It does not check if the plugin is actually enabled for the sandbox.
 */
function isPluginFrontendSandboxElegible({ isAngular }: { isAngular?: boolean }): boolean {
  // Only if the feature is not enabled no support for sandbox
  if (!Boolean(config.featureToggles.pluginsFrontendSandbox)) {
    return false;
  }

  // no support for angular plugins
  if (isAngular) {
    return false;
  }

  // To fast-test and debug the sandbox in the browser (dev mode only).
  const sandboxDisableQueryParam = location.search.includes('nosandbox') && config.buildInfo.env === 'development';
  if (sandboxDisableQueryParam) {
    return false;
  }

  // no sandbox in test mode. it often breaks e2e tests
  if (process.env.NODE_ENV === 'test') {
    return false;
  }

  return true;
}

/**
 * Check if the plugin is enabled for the sandbox via configuration.
 */
async function isPluginFrontendSandboxEnabled({ pluginId }: SandboxEligibilityCheckParams): Promise<boolean> {
  return Boolean(config.enableFrontendSandboxForPlugins?.includes(pluginId));
}
