import type { FaqItem, SectionCopy } from './types';

export const faqCopy: SectionCopy = {
  heading: 'Questions',
  leadHtml: 'Answer what a visitor asks before they install. Every entry is also emitted as structured data, so keep the plain-text answer complete on its own.',
};

// `a` is plain text and goes into the FAQ structured data and /llms.txt;
// `aHtml` is the rendered version and may add links and <code>. Keep the two
// saying the same thing — a search result quoting the plain answer and a page
// showing a different one is the failure this pairing exists to avoid.
export const faq: FaqItem[] = [
  {
    group: 'Basics',
    q: 'What is Example?',
    a: 'Example is a small command-line tool for doing one thing. It runs on Windows, macOS and Linux, prints readable output by default, and prints JSON when a program is reading.',
    aHtml:
      '<p>Example is a small command-line tool for doing one thing. It runs on Windows, macOS and Linux, prints readable output by default, and prints JSON when a program is reading.</p>',
  },
  {
    group: 'Basics',
    q: 'Does it need anything else installed?',
    a: 'No. The binary is self-contained on all three platforms; there is no runtime to install alongside it.',
    aHtml:
      '<p>No. The binary is self-contained on all three platforms; there is no runtime to install alongside it.</p>',
  },
  {
    group: 'Using it',
    q: 'Can I use it from a script?',
    a: 'Yes. Pass --json and every command prints machine-readable output on stdout, with errors and progress on stderr.',
    aHtml:
      '<p>Yes. Pass <code>--json</code> and every command prints machine-readable output on stdout, with errors and progress on stderr.</p>',
  },
];
