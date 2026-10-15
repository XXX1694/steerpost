import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/id/nav.json';
import language from '../../../messages/id/language.json';
import common from '../../../messages/id/common.json';
import errors from '../../../messages/id/errors.json';
import shell from '../../../messages/id/shell.json';
import legal from '../../../messages/id/legal.json';
import auth from '../../../messages/id/auth.json';
import posts from '../../../messages/id/posts.json';
import settings from '../../../messages/id/settings.json';
import dashboard from '../../../messages/id/dashboard.json';
import accounts from '../../../messages/id/accounts.json';
import analytics from '../../../messages/id/analytics.json';
import approvals from '../../../messages/id/approvals.json';
import calendar from '../../../messages/id/calendar.json';
import composer from '../../../messages/id/composer.json';
import media from '../../../messages/id/media.json';
import developer from '../../../messages/id/developer.json';
import agentAccess from '../../../messages/id/agentAccess.json';

/** id, assembled from messages/id/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
