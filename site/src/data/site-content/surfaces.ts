import type { SectionCopy, SurfaceCard } from './types';

export const surfacesCopy: SectionCopy = {
  heading: 'Two ways to use it',
  leadHtml:
    'One card per surface your product has. Cards alternate sides down the page; set <code>flip</code> to start on the right.',
};

export const surfaces: SurfaceCard[] = [
  {
    title: 'In the terminal',
    body: 'A card with an image panel. Give the intrinsic width and height so the browser reserves the space before the file arrives, and write alt text that says what the picture shows.',
    image: {
      src: '/assets/hero.png',
      alt: 'The Example command-line tool running in a terminal',
      width: 1640,
      height: 930,
    },
  },
  {
    title: 'In a script',
    body: 'A card with a code panel instead of an image. The markup is yours; colour it with the shared code tokens so it follows the theme.',
    flip: true,
    codeHtml: `<span style="color:var(--ui-code-comment)">$</span> example run --json
{
  <span style="color:var(--ui-code-key)">"status"</span>: <span style="color:var(--ui-code-string)">"ok"</span>,
  <span style="color:var(--ui-code-key)">"items"</span>: <span style="color:var(--ui-code-punct)">12</span>
}`,
  },
];
