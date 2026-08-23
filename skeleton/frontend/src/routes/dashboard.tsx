import { useTranslate } from "../i18n/provider";

/** Dashboard is the application surface. It renders behind authentication,
 * never reaches the static output, and therefore declares no metadata. */
export function Dashboard() {
  const t = useTranslate();
  return (
    <>
      <h1>{t("dashboard.title")}</h1>
      <p>{t("dashboard.body")}</p>
      <p>{t("notifications.count", { count: 3 })}</p>
    </>
  );
}
