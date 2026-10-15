import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/ru/nav.json';
import language from '../../../messages/ru/language.json';
import common from '../../../messages/ru/common.json';
import errors from '../../../messages/ru/errors.json';
import shell from '../../../messages/ru/shell.json';
import legal from '../../../messages/ru/legal.json';
import auth from '../../../messages/ru/auth.json';
import posts from '../../../messages/ru/posts.json';
import settings from '../../../messages/ru/settings.json';
import dashboard from '../../../messages/ru/dashboard.json';
import accounts from '../../../messages/ru/accounts.json';
import analytics from '../../../messages/ru/analytics.json';
import approvals from '../../../messages/ru/approvals.json';
import calendar from '../../../messages/ru/calendar.json';
import composer from '../../../messages/ru/composer.json';
import media from '../../../messages/ru/media.json';
import developer from '../../../messages/ru/developer.json';
import agentAccess from '../../../messages/ru/agentAccess.json';

/** ru, assembled from messages/ru/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
