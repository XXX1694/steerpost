/**
 * The message bundles: one JSON file per top-level namespace in messages/{locale}/{bundle}.json. English is the source,
 * so this list and the type below mirror messages/en/. `npm run i18n:check` fails when they differ from the files on disk.
 */
export const BUNDLES = [
  'nav',
  'language',
  'common',
  'errors',
  'shell',
  'legal',
  'auth',
  'posts',
  'settings',
  'dashboard',
  'accounts',
  'analytics',
  'approvals',
  'calendar',
  'composer',
  'media',
  'developer',
  'agentAccess',
] as const;

/** The shape of the English catalog; every key path is typed from it. */
export type Messages = {
  nav: typeof import('../../messages/en/nav.json');
  language: typeof import('../../messages/en/language.json');
  common: typeof import('../../messages/en/common.json');
  errors: typeof import('../../messages/en/errors.json');
  shell: typeof import('../../messages/en/shell.json');
  legal: typeof import('../../messages/en/legal.json');
  auth: typeof import('../../messages/en/auth.json');
  posts: typeof import('../../messages/en/posts.json');
  settings: typeof import('../../messages/en/settings.json');
  dashboard: typeof import('../../messages/en/dashboard.json');
  accounts: typeof import('../../messages/en/accounts.json');
  analytics: typeof import('../../messages/en/analytics.json');
  approvals: typeof import('../../messages/en/approvals.json');
  calendar: typeof import('../../messages/en/calendar.json');
  composer: typeof import('../../messages/en/composer.json');
  media: typeof import('../../messages/en/media.json');
  developer: typeof import('../../messages/en/developer.json');
  agentAccess: typeof import('../../messages/en/agentAccess.json');
};
