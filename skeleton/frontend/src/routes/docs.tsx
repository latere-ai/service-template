import { useTranslate } from "../i18n/provider";

export function Docs() {
  const t = useTranslate();
  return (
    <>
      <h1>{t("docs.title")}</h1>
      <p>{t("docs.body")}</p>
    </>
  );
}
