import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/es/nav.json';
import language from '../../../messages/es/language.json';
import common from '../../../messages/es/common.json';
import errors from '../../../messages/es/errors.json';
import shell from '../../../messages/es/shell.json';
import legal from '../../../messages/es/legal.json';
import auth from '../../../messages/es/auth.json';
import posts from '../../../messages/es/posts.json';
import settings from '../../../messages/es/settings.json';
import dashboard from '../../../messages/es/dashboard.json';
import accounts from '../../../messages/es/accounts.json';
import analytics from '../../../messages/es/analytics.json';
import approvals from '../../../messages/es/approvals.json';
import calendar from '../../../messages/es/calendar.json';
import composer from '../../../messages/es/composer.json';
import media from '../../../messages/es/media.json';
import developer from '../../../messages/es/developer.json';
import agentAccess from '../../../messages/es/agentAccess.json';

/** es, assembled from messages/es/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
