// afdocs agent-friendliness checks, one vitest case per check.
//
// Two ways to run it, both through this one file so the two runs cannot drift:
//
//   npm run test:agent-docs         checks https://agentsummons.dev
//   npm run test:agent-docs:local   checks the site built from this tree
//
// The local run goes through ./check_agent_docs, which starts `hugo server`
// and passes its URL in AGENT_DOCS_URL. Everything else (the production URL,
// the markdown-content-parity exclusions) comes from agent-docs.config.yml, so
// there is one config file rather than a local copy to keep in sync.
import { describeAgentDocsPerCheck, loadConfig } from 'afdocs/helpers';

// A slow runner scanning 11 pages can outlast the helper's 120s default.
const TIMEOUT_MS = 300_000;

// Apache on Dreamhost serves markdown for Accept: text/markdown and sets the
// real Cache-Control headers. A local server knows nothing about either, so
// running these against the build would measure `hugo server` rather than the
// site. The live run is what covers them.
const SERVER_ONLY_CHECKS = ['content-negotiation', 'cache-header-hygiene'];

const config = await loadConfig(import.meta.dirname);
const localUrl = process.env.AGENT_DOCS_URL;

describeAgentDocsPerCheck(
  localUrl
    ? {
        ...config,
        url: localUrl,
        skipChecks: [...(config.skipChecks ?? []), ...SERVER_ONLY_CHECKS],
      }
    : config,
  TIMEOUT_MS,
);
