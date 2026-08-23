import { formatCurrency } from "../lib/format";
import { useLocale, useTranslate } from "../i18n/provider";

export function Pricing() {
  const t = useTranslate();
  const locale = useLocale();
  return (
    <>
      <h1>{t("pricing.title")}</h1>
      <p>{t("pricing.body")}</p>
      <p>{formatCurrency(locale, 49, "EUR")}</p>
    </>
  );
}
