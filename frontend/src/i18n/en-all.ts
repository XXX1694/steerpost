import type { Messages } from '@/i18n/catalog';
import nav from '../../messages/en/nav.json';
import language from '../../messages/en/language.json';
import common from '../../messages/en/common.json';
import errors from '../../messages/en/errors.json';
import shell from '../../messages/en/shell.json';
import legal from '../../messages/en/legal.json';
import auth from '../../messages/en/auth.json';
import posts from '../../messages/en/posts.json';
import settings from '../../messages/en/settings.json';
import dashboard from '../../messages/en/dashboard.json';
import accounts from '../../messages/en/accounts.json';
import analytics from '../../messages/en/analytics.json';
import approvals from '../../messages/en/approvals.json';
import calendar from '../../messages/en/calendar.json';
import composer from '../../messages/en/composer.json';
import media from '../../messages/en/media.json';
import developer from '../../messages/en/developer.json';
import agentAccess from '../../messages/en/agentAccess.json';

/** The whole English catalog, assembled from its bundles. Imported statically, so English arrives with the page. */
const en: Messages = {
  nav,
  language,
  common,
  errors,
  shell,
  legal,
  auth,
  posts,
  settings,
  dashboard,
  accounts,
  analytics,
  approvals,
  calendar,
  composer,
  media,
  developer,
  agentAccess,
};

export default en;
