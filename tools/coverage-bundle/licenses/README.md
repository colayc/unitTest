# Python/gcovr 许可与 NOTICE 合同

本目录保存随 coverage bundle 分发时必须保留的 Python `3.14.6` 与 gcovr `8.6` license 文本。`dependencies.json` 以精确 package version 记录每个已锁定 dependency 的 SPDX license、上游一手 license URL、适用 license 文本和 NOTICE。

Task 2 组装 application archive 时必须从已验证 wheel 中保留其 license/NOTICE，并与这里的合同交叉核验；不得依赖安装机器、registry 或未锁定的 pip metadata。特别是 lxml binary wheel 所含的 libxml2/libxslt material 必须保留随 wheel 提供的 NOTICE。

本合同只覆盖 manifest 当前的闭合 dependency lock。升级、添加 package 或改动 wheel 平台选择时，必须同步更新 `dependencies.json`、对应 license evidence 和 `manifest.test.mjs`。
