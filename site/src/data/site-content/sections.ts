import type { LandingLayout, LandingSection } from './types';

// Which sections the landing page renders, in order.
//
// The four are independent: each reads its own content file and none assumes
// what sits above it. So a product with no install story drops 'install'
// rather than filling it with something weak, and one whose visitors ask
// questions before they read features can put 'faq' second.
//
// A name that appears twice renders twice; a name whose content is empty
// renders nothing. Dropping the last section is fine -- the footer is not in
// this list, and neither is the nav.
// 'reach' is deliberately absent. It and 'compare' were two sections making
// the same argument from different angles, and the matrix is the one a
// reader expects. The reach content and component are kept, so putting it
// back is one word.
export const sections: LandingSection[] = ['hero', 'surfaces', 'compare', 'policy', 'install', 'faq'];

// How the landing page is laid out above the fold, and the rhythm that
// follows from it.
//
//   'centered' -- everything on the centre line: badge, headline, subhead,
//   the two commands, then a full-width screenshot below. Reads as a launch
//   page. It is what netscli uses.
//
//   'split' -- copy left, screenshot right, both aligned to the same
//   baseline, and the sections below sit closer together. Reads as a working
//   tool's page: less announcement, more "here it is".
//
// Both use the same content and the same components. Below 900px they are
// the same page, because there is only one sensible arrangement of a
// headline and a picture on a phone.
export const landingLayout: LandingLayout = 'split';
