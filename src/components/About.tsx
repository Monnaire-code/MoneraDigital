import { useTranslation } from "react-i18next";
import { useFundStats } from "@/hooks/use-fund-stats";
import { formatUsdShort } from "@/lib/locale-format";

const About = () => {
  const { t, i18n } = useTranslation();
  const isZh = i18n.language === "zh";
  const lang = i18n.language;
  const { data: fundData } = useFundStats();

  const aumDisplay = fundData ? formatUsdShort(fundData.current.totalAum, lang) : "—";

  return (
    <section id="about" className="py-24 relative">
      <div className="container mx-auto px-6">
        <div className="max-w-4xl mx-auto text-center">
          <h2 className="text-3xl sm:text-4xl font-bold text-foreground mb-6">
            {isZh ? "关于 Monera Digital" : "About Monera Digital"}
          </h2>
          <p className="text-muted-foreground text-lg leading-relaxed mb-8">
            {isZh
              ? "Monera Digital 是领先的加密资产财富管理平台，为机构投资者和高净值个人提供专业级的数字资产解决方案。我们致力于通过创新的结构化产品和卓越的服务，帮助客户实现资产的稳健增长。"
              : "Monera Digital is a leading cryptocurrency wealth management platform providing institutional-grade digital asset solutions for institutional investors and high-net-worth individuals. We are committed to helping clients achieve steady asset growth through innovative structured products and excellent service."
            }
          </p>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-6 mt-12">
            <div className="text-center">
              <div className="text-3xl font-bold text-primary">50+</div>
              <div className="text-sm text-muted-foreground">{isZh ? "机构客户" : "Institutional Clients"}</div>
            </div>
            <div className="text-center">
              <div className="text-3xl font-bold text-primary" data-testid="about-aum-value">
                {aumDisplay}
              </div>
              <div className="text-sm text-muted-foreground">{isZh ? "管理资产" : "Assets Under Management"}</div>
            </div>
            <div className="text-center">
              <div className="text-3xl font-bold text-primary">99.9%</div>
              <div className="text-sm text-muted-foreground">{isZh ? "系统稳定性" : "System Stability"}</div>
            </div>
            <div className="text-center">
              <div className="text-3xl font-bold text-primary">24/7</div>
              <div className="text-sm text-muted-foreground">{isZh ? "客户支持" : "Customer Support"}</div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
};

export default About;
