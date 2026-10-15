import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/de/nav.json';
import language from '../../../messages/de/language.json';
import common from '../../../messages/de/common.json';
import errors from '../../../messages/de/errors.json';
import shell from '../../../messages/de/shell.json';
import legal from '../../../messages/de/legal.json';
import auth from '../../../messages/de/auth.json';
import posts from '../../../messages/de/posts.json';
import settings from '../../../messages/de/settings.json';
import dashboard from '../../../messages/de/dashboard.json';
import accounts from '../../../messages/de/accounts.json';
import analytics from '../../../messages/de/analytics.json';
import approvals from '../../../messages/de/approvals.json';
import calendar from '../../../messages/de/calendar.json';
import composer from '../../../messages/de/composer.json';
import media from '../../../messages/de/media.json';
import developer from '../../../messages/de/developer.json';
import agentAccess from '../../../messages/de/agentAccess.json';

/** de, assembled from messages/de/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
