import type { Catalog } from '@/i18n/pseudo';
import nav from '../../../messages/zh-CN/nav.json';
import language from '../../../messages/zh-CN/language.json';
import common from '../../../messages/zh-CN/common.json';
import errors from '../../../messages/zh-CN/errors.json';
import shell from '../../../messages/zh-CN/shell.json';
import legal from '../../../messages/zh-CN/legal.json';
import auth from '../../../messages/zh-CN/auth.json';
import posts from '../../../messages/zh-CN/posts.json';
import settings from '../../../messages/zh-CN/settings.json';
import dashboard from '../../../messages/zh-CN/dashboard.json';
import accounts from '../../../messages/zh-CN/accounts.json';
import analytics from '../../../messages/zh-CN/analytics.json';
import approvals from '../../../messages/zh-CN/approvals.json';
import calendar from '../../../messages/zh-CN/calendar.json';
import composer from '../../../messages/zh-CN/composer.json';
import media from '../../../messages/zh-CN/media.json';
import developer from '../../../messages/zh-CN/developer.json';
import agentAccess from '../../../messages/zh-CN/agentAccess.json';

/** zh-CN, assembled from messages/zh-CN/*.json. One module per locale, so the build emits one lazy chunk per locale. `npm run i18n:check` keeps this list equal to the files on disk. */
const catalog: Catalog = { nav, language, common, errors, shell, legal, auth, posts, settings, dashboard, accounts, analytics, approvals, calendar, composer, media, developer, agentAccess };

export default catalog;
