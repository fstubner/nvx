import type { APIRoute } from 'astro';
import { meta } from '../data/site-content/meta';

/* robots.txt built from the site's own domain rather than kept as a static
 * file that names one. The Starlight/@astrojs/sitemap integration generates
 * sitemap-index.xml on every build, so the pointer is always current. */
export const GET: APIRoute = () =>
  new Response(
    [
      'User-agent: *',
      'Allow: /',
      '',
      `Sitemap: ${meta.domain}/sitemap-index.xml`,
      '',
    ].join('\n'),
    { headers: { 'Content-Type': 'text/plain; charset=utf-8' } },
  );
