package main

import (
	"fmt"

	pb "exec_platform_test/proto"
)

func main() {
	msg := &pb.Hello{Message: "hello from protobuf"}
	fmt.Println(msg.GetMessage())
}
