import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/ja/nav.json';
import language from '../../../messages/ja/language.json';
import common from '../../../messages/ja/common.json';
import errors from '../../../messages/ja/errors.json';
import shell from '../../../messages/ja/shell.json';
import legal from '../../../messages/ja/legal.json';
import auth from '../../../messages/ja/auth.json';
import posts from '../../../messages/ja/posts.json';
import settings from '../../../messages/ja/settings.json';
import dashboard from '../../../messages/ja/dashboard.json';
import accounts from '../../../messages/ja/accounts.json';
import analytics from '../../../messages/ja/analytics.json';
import approvals from '../../../messages/ja/approvals.json';
import calendar from '../../../messages/ja/calendar.json';
import composer from '../../../messages/ja/composer.json';
import media from '../../../messages/ja/media.json';
import developer from '../../../messages/ja/developer.json';
import agentAccess from '../../../messages/ja/agentAccess.json';

/** ja, assembled from messages/ja/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
