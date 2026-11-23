package main

import (
	"ai-style-backend/pb"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	// 引入你生成的 pb 包（请把 ai-style-backend 替换为你go.mod里的模块名）

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// 1. 连接 Docker 里的 Python gRPC 服务 (监听在本地 50051 端口)
	// 注意：Go 1.24+ / gRPC v1.60+ 推荐使用 grpc.NewClient，旧版本可用 grpc.Dial
	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("连接 Python gRPC 服务失败: %v", err)
	}
	defer conn.Close()

	// 2. 创建 gRPC 客户端实例
	client := pb.NewFaceAnalyzerClient(conn)

	// 3. 读取一张本地测试图片（请确保同目录下有一张名为 1.jpg 的图片）
	imagePath := "7.jpg"
	imgBytes, err := os.ReadFile(imagePath)
	if err != nil {
		log.Fatalf("读取本地图片失败 [%s]: %v", imagePath, err)
	}

	// 4. 构造 gRPC 请求对象
	req := &pb.FaceRequest{
		ImageData: imgBytes,
	}

	// 5. 设置超时上下文并发起远程调用
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fmt.Printf("📤 正在发送图片 (%d 字节) 到 Python 算法容器...\n", len(imgBytes))

	resp, err := client.AnalyzeFace(ctx, req)
	if err != nil {
		log.Fatalf("调用 AnalyzeFace 失败: %v", err)
	}

	// 6. 打印 Python 端返回的分析结果
	fmt.Println("================== 收到 Python 服务响应 ==================")
	fmt.Printf("消息状态: %s\n", resp.GetMessage())
	fmt.Printf("脸型识别: %s\n", resp.GetFaceShape())
	fmt.Printf("脸长宽比: %.3f\n", resp.GetFaceRatio())
	fmt.Printf("三庭比例: 上庭=%.2f, 中庭=%.2f, 下庭=%.2f\n",
		resp.GetUpperThird(), resp.GetMiddleThird(), resp.GetLowerThird())
	fmt.Println("========================================================")
}
