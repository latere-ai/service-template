import { useTranslate } from "../i18n/provider";

export function Home() {
  const t = useTranslate();
  return (
    <>
      <h1>{t("home.title")}</h1>
      <p>{t("home.body")}</p>
    </>
  );
}
