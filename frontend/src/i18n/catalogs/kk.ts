import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/kk/nav.json';
import language from '../../../messages/kk/language.json';
import common from '../../../messages/kk/common.json';
import errors from '../../../messages/kk/errors.json';
import shell from '../../../messages/kk/shell.json';
import legal from '../../../messages/kk/legal.json';
import auth from '../../../messages/kk/auth.json';
import posts from '../../../messages/kk/posts.json';
import settings from '../../../messages/kk/settings.json';
import dashboard from '../../../messages/kk/dashboard.json';
import accounts from '../../../messages/kk/accounts.json';
import analytics from '../../../messages/kk/analytics.json';
import approvals from '../../../messages/kk/approvals.json';
import calendar from '../../../messages/kk/calendar.json';
import composer from '../../../messages/kk/composer.json';
import media from '../../../messages/kk/media.json';
import developer from '../../../messages/kk/developer.json';
import agentAccess from '../../../messages/kk/agentAccess.json';

/** kk, assembled from messages/kk/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
