// The feedback block's shape, split out of types.ts.
//
// Not a judgement about feedback being special: types.ts was 302 lines against
// a 300-line guard and this was the most self-contained 33 of them. The
// comparison table's types are the next candidate if it goes over again.

/** One feedback route: a labelled link that opens a prefilled GitHub issue. */
export interface FeedbackRoute {
  /** Button text. */
  label: string;
  /**
   * Issue form filename in `.github/ISSUE_TEMPLATE/`, e.g. `bug.yml`.
   *
   * Optional. GitHub ignores a template that does not exist and opens a blank
   * issue instead, so a wrong name degrades to a working link rather than a
   * broken one — which also means nothing here warns you about a typo.
   */
  template?: string;
  /** Labels applied to the new issue. The repo need not already define them. */
  issueLabels?: string[];
}

export interface Feedback {
  /** Whether the feedback block and its footer link render at all. */
  enabled: boolean;
  /** The line above the buttons. */
  prompt: string;
  problem: FeedbackRoute;
  idea: FeedbackRoute;
  /**
   * Repo that receives the issues, "owner/name".
   *
   * Defaults to `social.repo`. Set it only when feedback belongs somewhere
   * other than the product's own repository.
   */
  repo?: string;
}
