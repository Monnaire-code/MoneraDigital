import { Construction } from "lucide-react";
import { useTranslation } from "react-i18next";

interface ComingSoonProps {
  moduleName?: string;
}

const ComingSoon = ({ moduleName }: ComingSoonProps) => {
  const { t } = useTranslation();
  const name = moduleName || "此功能";

  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] gap-4">
      <Construction className="w-16 h-16 text-muted-foreground" />
      <h2 className="text-2xl font-bold">{name}</h2>
      <p className="text-muted-foreground">功能开发中，敬请期待</p>
    </div>
  );
};

export default ComingSoon;
