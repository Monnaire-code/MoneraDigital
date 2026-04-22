import { useTranslation } from "react-i18next";
import { TrendingUp, Newspaper, BookOpen, Video } from "lucide-react";

const Insights = () => {
  const { t, i18n } = useTranslation();
  const isZh = i18n.language === "zh";

  const articles = [
    {
      icon: <TrendingUp className="w-6 h-6" />,
      title: isZh ? "加密货币市场趋势分析" : "Cryptocurrency Market Trend Analysis",
      description: isZh ? "深入解析当前市场动态与投资机会" : "In-depth analysis of current market dynamics and investment opportunities",
      date: isZh ? "2026年4月" : "April 2026",
    },
    {
      icon: <Newspaper className="w-6 h-6" />,
      title: isZh ? "机构级数字资产托管指南" : "Institutional Digital Asset Custody Guide",
      description: isZh ? "了解机构级资产托管的最佳实践" : "Learn best practices for institutional-grade asset custody",
      date: isZh ? "2026年3月" : "March 2026",
    },
    {
      icon: <BookOpen className="w-6 h-6" />,
      title: isZh ? "结构化产品入门教程" : "Structured Products Introduction",
      description: isZh ? "从零开始了解结构化收益产品" : "Learn structured yield products from scratch",
      date: isZh ? "2026年2月" : "February 2026",
    },
  ];

  return (
    <section id="insights" className="py-24 relative bg-muted/30">
      <div className="container mx-auto px-6">
        <div className="text-center mb-12">
          <h2 className="text-3xl sm:text-4xl font-bold text-foreground mb-4">
            {isZh ? "洞见" : "Insights"}
          </h2>
          <p className="text-muted-foreground max-w-2xl mx-auto">
            {isZh 
              ? "获取最新的行业资讯、投资策略和市场分析"
              : "Get the latest industry news, investment strategies and market analysis"
            }
          </p>
        </div>

        <div className="grid md:grid-cols-3 gap-6 max-w-5xl mx-auto">
          {articles.map((article, index) => (
            <div 
              key={index}
              className="bg-background rounded-xl p-6 border border-border hover:border-primary/50 hover:shadow-lg transition-all duration-300 cursor-pointer"
            >
              <div className="w-12 h-12 rounded-lg bg-primary/10 flex items-center justify-center text-primary mb-4">
                {article.icon}
              </div>
              <div className="text-xs text-muted-foreground mb-2">{article.date}</div>
              <h3 className="text-lg font-semibold text-foreground mb-2">{article.title}</h3>
              <p className="text-sm text-muted-foreground">{article.description}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
};

export default Insights;
