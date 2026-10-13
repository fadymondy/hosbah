// Desktop-only strings (ar + en) layered over the shared catalogue: `tr` looks here first, then
// falls back to @hosbah/client/i18n. Used by the main process (tray, notifications) and the renderer.
import type { Locale } from "@hosbah/client";
import { t as sharedT, type MessageKey } from "@hosbah/client/i18n";

const en = {
  "app.open": "Open Hosbah",
  "app.quit": "Quit Hosbah",
  "tray.recent": "Recent alerts",
  "tray.none": "No alerts",
  "tray.unreadCount": "{count} unread alerts",
  "tray.tooltip": "Hosbah: {status}",

  "login.subtitle": "Servers, apps and alerts, in your tray.",
  "login.advanced": "Server address",
  "login.2faHelp": "Enter the code from your authenticator app.",
  "login.useCode": "Use an authentication code",
  "login.recoveryCode": "Recovery code",
  "login.signingIn": "Signing in...",
  "login.localOnly": "Test mode: only a local server is allowed.",
  "login.error": "That email and password did not match.",

  "nav.signedInAs": "Signed in as {email}",

  "overview.onlineOf": "{online} of {total} online",
  "overview.runningOf": "{running} of {total} running",
  "overview.certsSoon": "{count} expiring soon",
  "overview.backupsOk": "{ok} of {total} healthy",
  "overview.allClear": "All clear",
  "overview.attention": "Needs attention",

  "apps.empty": "No apps yet",
  "app.lastDeploy": "Last deploy",
  "app.server": "Server",
  "app.branch": "Branch",
  "app.repo": "Repository",
  "app.status": "Status",
  "app.noLastDeploy": "Never deployed",
  "app.back": "All apps",
  "app.actions": "Actions",
  "app.viewOnlyRole": "Your role ({role}) can view but not change this app.",
  "app.processes": "Processes",

  "server.back": "Overview",
  "server.apps": "Apps on this server",
  "server.detail": "Server details",
  "server.os": "System",
  "server.ip": "IP address",
  "server.agent": "Agent",
  "server.history": "Last hour",
  "server.noApps": "No apps on this server",

  "confirm.title": "Confirm action",
  "confirm.type": "Type {name} to confirm",
  "confirm.desc.redeploy": "Builds and releases the latest commit of the branch. Traffic switches only after the new release is healthy.",
  "confirm.desc.restart": "Restarts the live release in fresh containers. Expect a short interruption.",
  "confirm.desc.start": "Starts the app's containers.",
  "confirm.desc.stop": "Stops the app's containers. The app will be offline until you start it again.",
  "confirm.working": "Working...",
  "confirm.touchId": "macOS will ask for Touch ID or your password.",
  "action.failed": "Could not {action}",
  "action.ok": "{action}: requested",

  "deploy.trigger": "Trigger",
  "deploy.commit": "Commit",
  "deploy.noLogs": "No log lines yet",
  "deploy.hideLogs": "Hide logs",
  "deploy.kind.deploy": "Deploy",
  "deploy.kind.restart": "Restart",
  "deploy.kind.rollback": "Rollback",
  "deploy.number": "#{number}",

  "domain.host": "Domain",
  "domain.app": "App",
  "domain.certificate": "Certificate",
  "domain.expires": "Expires",
  "domain.issuer": "Issuer",
  "domain.noCert": "Not checked yet",
  "domain.daysLeft": "{days} days left",

  "backup.service": "Database",
  "backup.size": "Size",
  "backup.next": "Next run",
  "backup.count": "{count} backups",
  "backup.noBackups": "No backups configured",
  "backup.target": "Target",
  "backup.history": "Recent backups",
  "backup.manual": "Manual",
  "backup.scheduled": "Scheduled",
  "backup.succeeded": "Succeeded",
  "backup.pending": "Pending",
  "backup.noneYet": "No backups yet",

  "alerts.open": "Open",
  "alerts.allRead": "All caught up",

  "settings.general": "General",
  "settings.notifications.desc": "Choose which events show a desktop notification.",
  "settings.startAtLogin": "Start Hosbah when I sign in",
  "settings.serverUrl.hint": "Use http://localhost:8710 for a local stack. Changing it signs you out.",
  "settings.serverUrl.save": "Save and sign out",
  "settings.serverUrl.invalid": "That is not a valid address",
  "settings.account": "Account",
  "settings.about": "About",
  "settings.saved": "Saved",

  "common.offline": "Cannot reach the server. Showing what we last knew.",
  "common.dash": "-",
  "common.search": "Search",
} as const;

const ar: Record<keyof typeof en, string> = {
  "app.open": "فتح حوسبة",
  "app.quit": "إنهاء حوسبة",
  "tray.recent": "آخر التنبيهات",
  "tray.none": "لا توجد تنبيهات",
  "tray.unreadCount": "{count} تنبيهات غير مقروءة",
  "tray.tooltip": "حوسبة: {status}",

  "login.subtitle": "الخوادم والتطبيقات والتنبيهات في شريط النظام.",
  "login.advanced": "عنوان الخادم",
  "login.2faHelp": "أدخل الرمز من تطبيق المصادقة.",
  "login.useCode": "استخدم رمز المصادقة",
  "login.recoveryCode": "رمز الاسترداد",
  "login.signingIn": "جارٍ تسجيل الدخول...",
  "login.localOnly": "وضع الاختبار: الخادم المحلي فقط.",
  "login.error": "البريد الإلكتروني أو كلمة المرور غير صحيحة",

  "nav.signedInAs": "مسجّل الدخول باسم {email}",

  "overview.onlineOf": "{online} من {total} متصل",
  "overview.runningOf": "{running} من {total} يعمل",
  "overview.certsSoon": "{count} تنتهي قريبًا",
  "overview.backupsOk": "{ok} من {total} سليمة",
  "overview.allClear": "كل شيء سليم",
  "overview.attention": "يحتاج إلى انتباه",

  "apps.empty": "لا توجد تطبيقات بعد",
  "app.lastDeploy": "آخر نشر",
  "app.server": "الخادم",
  "app.branch": "الفرع",
  "app.repo": "المستودع",
  "app.status": "الحالة",
  "app.noLastDeploy": "لم يُنشر بعد",
  "app.back": "كل التطبيقات",
  "app.actions": "الإجراءات",
  "app.viewOnlyRole": "دورك ({role}) يتيح العرض فقط لهذا التطبيق.",
  "app.processes": "العمليات",

  "server.back": "نظرة عامة",
  "server.apps": "التطبيقات على هذا الخادم",
  "server.detail": "تفاصيل الخادم",
  "server.os": "النظام",
  "server.ip": "عنوان IP",
  "server.agent": "الوكيل",
  "server.history": "آخر ساعة",
  "server.noApps": "لا توجد تطبيقات على هذا الخادم",

  "confirm.title": "تأكيد الإجراء",
  "confirm.type": "اكتب {name} للتأكيد",
  "confirm.desc.redeploy": "يبني ويُصدر آخر إصدار من الفرع. تنتقل الحركة بعد أن يصبح الإصدار الجديد سليمًا.",
  "confirm.desc.restart": "يعيد تشغيل الإصدار الحالي في حاويات جديدة. توقّع انقطاعًا قصيرًا.",
  "confirm.desc.start": "يشغّل حاويات التطبيق.",
  "confirm.desc.stop": "يوقف حاويات التطبيق. سيبقى التطبيق متوقفًا حتى تشغّله من جديد.",
  "confirm.working": "جارٍ التنفيذ...",
  "confirm.touchId": "سيطلب macOS بصمة Touch ID أو كلمة المرور.",
  "action.failed": "تعذّر التنفيذ: {action}",
  "action.ok": "{action}: تم الطلب",

  "deploy.trigger": "المُشغِّل",
  "deploy.commit": "الإيداع",
  "deploy.noLogs": "لا توجد أسطر سجل بعد",
  "deploy.hideLogs": "إخفاء السجل",
  "deploy.kind.deploy": "نشر",
  "deploy.kind.restart": "إعادة تشغيل",
  "deploy.kind.rollback": "تراجع",
  "deploy.number": "#{number}",

  "domain.host": "النطاق",
  "domain.app": "التطبيق",
  "domain.certificate": "الشهادة",
  "domain.expires": "تنتهي",
  "domain.issuer": "الجهة المُصدِرة",
  "domain.noCert": "لم تُفحص بعد",
  "domain.daysLeft": "تبقّى {days} يومًا",

  "backup.service": "قاعدة البيانات",
  "backup.size": "الحجم",
  "backup.next": "التشغيل التالي",
  "backup.count": "{count} نسخ",
  "backup.noBackups": "لا توجد نسخ احتياطية مُعدّة",
  "backup.target": "الوجهة",
  "backup.history": "آخر النسخ",
  "backup.manual": "يدوية",
  "backup.scheduled": "مجدولة",
  "backup.succeeded": "نجحت",
  "backup.pending": "قيد الانتظار",
  "backup.noneYet": "لا توجد نسخ بعد",

  "alerts.open": "فتح",
  "alerts.allRead": "لا شيء جديد",

  "settings.general": "عام",
  "settings.notifications.desc": "اختر الأحداث التي تُظهر إشعارًا على سطح المكتب.",
  "settings.startAtLogin": "تشغيل حوسبة عند تسجيل الدخول",
  "settings.serverUrl.hint": "استخدم http://localhost:8710 لبيئة محلية. تغيير العنوان يسجّل خروجك.",
  "settings.serverUrl.save": "حفظ وتسجيل الخروج",
  "settings.serverUrl.invalid": "هذا العنوان غير صالح",
  "settings.account": "الحساب",
  "settings.about": "حول",
  "settings.saved": "تم الحفظ",

  "common.offline": "تعذّر الوصول إلى الخادم. نعرض آخر ما نعرفه.",
  "common.dash": "-",
  "common.search": "بحث",
};

export type ExtraKey = keyof typeof en;
export type Key = ExtraKey | MessageKey;
export type { Locale };

const own: Record<Locale, Record<ExtraKey, string>> = { ar, en };

export function tr(locale: Locale, key: Key, vars?: Record<string, string | number>): string {
  const raw = (own[locale] as Record<string, string>)[key];
  if (raw === undefined) return sharedT(locale, key as MessageKey, vars);
  if (!vars) return raw;
  return raw.replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m));
}
