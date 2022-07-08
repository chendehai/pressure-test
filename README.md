# 此项目转移至 https://github.com/web3chain33/pressure-test
存证溯源的压力测试仓库

编译打包：
压测程序的编译指令 make + 压测程序名
config.toml为配置文件

清理:
make clean

压力测试平行链发行ERC721,目录在chain33/erc721
make erc721 编译打包

压力测试平行链转账 transfer, 目录在chain33/transfer
make transfer 编译打包

压力测试平行链存证 proof, 目录在chain33/proof
make proof 编译打包

