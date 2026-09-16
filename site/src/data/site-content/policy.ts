import type { PolicyPoint, SectionCopy } from './types';

// The controls an organisation asks about, which had no surface on this page
// at all until 2026-09-16. Every claim here is something nvx ships today, and
// every command named is one `nvx policy` or `nvx audit` actually accepts.
export const policyCopy: SectionCopy = {
  heading: 'A file in your repo, not a dashboard',
  leadHtml:
    'The rules live in <code>.nvx-policy.json</code>, next to the code they govern. Reviewed in a pull request, versioned with the project, and enforced on the machine. Nothing leaves it, and there is nothing to buy per seat.',
};

export const policyPoints: PolicyPoint[] = [
  {
    title: 'An org baseline a project can tighten, never loosen',
    body: 'Set <code>"enforced": true</code> in the global policy and a project file may only make things stricter. One that tries to widen the allowlist, lower the release-age window or turn the sandbox off is refused, and nvx names the field and both values rather than ignoring it quietly.',
  },
  {
    title: 'An agent cannot approve its own way out',
    body: '<code>-y</code>, <code>--agent-mode</code> and <code>NVX_YES</code> deliberately do not approve anything that widens the sandbox. That is the whole point when the thing running <code>npm install</code> is a coding agent with your credentials.',
  },
  {
    title: 'Evidence you can export',
    body: 'Every block, every scrubbed variable and every approval is recorded. <code>nvx audit export</code> writes them out as JSON, JSONL or CSV, and the field set of all 30 events is documented as a contract so a compliance pipeline can depend on it.',
  },
  {
    title: 'A check CI can fail on',
    body: '<code>nvx policy check</code> exits with a distinct code per failure class, so a pipeline can tell a blocked package from an advisory from an unreachable network. Network checks need <code>--online</code>, because a check that fails when OSV is down must not look like a violation.',
  },
];

/** Shown under the points. The exit codes are the ones in docs/exit-codes.md. */
export const policyNoteHtml =
  'Exit codes: <code>0</code> pass, <code>10</code> policy violation, <code>11</code> blocked package, <code>12</code> vulnerability, <code>13</code> release age, <code>14</code> sandbox unavailable, <code>15</code> policy file invalid.';
