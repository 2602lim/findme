# FindMe - 快速文件内容搜索工具



`FindMe`是一个快速的文件内容搜索工具,可以在系统的文件目录中搜索指定关键字,并输出匹配的文件及行号信息。该工具支持多种文件类型,包括文本文件和代码文件等,并提供并行搜索功能,提高搜索效率。

## 功能特性

- 支持文本文件和代码文件的搜索
- 支持多关键字搜索
- 支持自定义搜索目录和输出文件
- 支持并行搜索,提高搜索效率
- 支持搜索深度限制
- 支持最大文件大小限制
- 提供实时进度显示

## 安装使用

1. 下载可执行文件
2. 在命令行中运行`findme`命令,可查看帮助信息并了解各参数含义
3. 根据需求设置搜索参数,例如:
findme -path C:\Projects -keyword api_key,secret -type code
