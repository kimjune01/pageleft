package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const domain = "pageleft.cc"

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {

		// --- Default VPC ---
		defaultVpc, err := ec2.LookupVpc(ctx, &ec2.LookupVpcArgs{Default: pulumi.BoolRef(true)})
		if err != nil {
			return err
		}

		// --- SSH Key Pair ---
		keyPair, err := ec2.NewKeyPair(ctx, "pageleft-key", &ec2.KeyPairArgs{
			PublicKey: pulumi.String("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBeJoahllCWwnBjXSl5yol3LUw6FtcaLVfhUEIZO8z5P kimjune01@gmail.com"),
		})
		if err != nil {
			return err
		}

		// --- Security Group ---
		sg, err := ec2.NewSecurityGroup(ctx, "pageleft-sg", &ec2.SecurityGroupArgs{
			VpcId:       pulumi.String(defaultVpc.Id),
			Description: pulumi.String("PageLeft - HTTP, HTTPS, SSH"),
			Ingress: ec2.SecurityGroupIngressArray{
				&ec2.SecurityGroupIngressArgs{
					Protocol:   pulumi.String("tcp"),
					FromPort:   pulumi.Int(80),
					ToPort:     pulumi.Int(80),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
				&ec2.SecurityGroupIngressArgs{
					Protocol:   pulumi.String("tcp"),
					FromPort:   pulumi.Int(443),
					ToPort:     pulumi.Int(443),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
				&ec2.SecurityGroupIngressArgs{
					Protocol:   pulumi.String("tcp"),
					FromPort:   pulumi.Int(22),
					ToPort:     pulumi.Int(22),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
			},
			Egress: ec2.SecurityGroupEgressArray{
				&ec2.SecurityGroupEgressArgs{
					Protocol:   pulumi.String("-1"),
					FromPort:   pulumi.Int(0),
					ToPort:     pulumi.Int(0),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
			},
		})
		if err != nil {
			return err
		}

		// --- Config ---
		cfg := config.New(ctx, "")
		hfToken := cfg.Require("hfToken")

		// --- User Data Script ---
		userData := fmt.Sprintf(`#!/bin/bash
set -euxo pipefail
exec > /var/log/user-data.log 2>&1

export DEBIAN_FRONTEND=noninteractive

# --- System packages ---
apt-get update
apt-get install -y git curl debian-keyring debian-archive-keyring apt-transport-https

# --- Go (latest stable via snap) ---
snap install go --classic

# --- Caddy ---
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt-get update
apt-get install -y caddy

# --- Clone repo ---
cd /opt
git clone https://github.com/kimjune01/pageleft.git
cd /opt/pageleft

# --- Build Go binary ---
export HOME=/root
/snap/bin/go build -o /usr/local/bin/pageleft ./cmd/pageleft

# --- Create data directory ---
mkdir -p /var/lib/pageleft

# --- systemd: server ---
cat > /etc/systemd/system/pageleft-server.service << 'UNIT'
[Unit]
Description=PageLeft Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/pageleft serve --port 8080
Restart=always
RestartSec=5
Environment=PAGELEFT_DB=/var/lib/pageleft/pageleft.db
Environment=HF_TOKEN=%s

[Install]
WantedBy=multi-user.target
UNIT

# --- Caddy config ---
cat > /etc/caddy/Caddyfile << 'CADDY'
pageleft.cc {
    reverse_proxy localhost:8080
}

www.pageleft.cc {
    redir https://pageleft.cc{uri} permanent
}
CADDY

# --- Enable and start services ---
systemctl daemon-reload
systemctl enable pageleft-server caddy
systemctl start pageleft-server
systemctl restart caddy

# --- Initial crawl + reindex ---
echo "Running initial crawl..."
PAGELEFT_DB=/var/lib/pageleft/pageleft.db \
HF_TOKEN=%s \
  /usr/local/bin/pageleft crawl --seeds "https://creativecommons.org,https://www.gnu.org,https://www.fsf.org" --max-pages 100 || true

echo "Running reindex..."
PAGELEFT_DB=/var/lib/pageleft/pageleft.db \
HF_TOKEN=%s \
  /usr/local/bin/pageleft reindex || true

echo "User data script complete!"
`, hfToken, hfToken, hfToken)

		// --- EC2 Instance ---
		instance, err := ec2.NewInstance(ctx, "pageleft-server", &ec2.InstanceArgs{
			Ami:                 pulumi.String("ami-0c0be557d3f581f0e"), // Ubuntu 24.04 ARM64, us-west-2
			InstanceType:        pulumi.String("t4g.micro"),
			KeyName:             keyPair.KeyName,
			VpcSecurityGroupIds: pulumi.StringArray{sg.ID()},
			UserData:            pulumi.String(userData),
			RootBlockDevice: &ec2.InstanceRootBlockDeviceArgs{
				VolumeSize:          pulumi.Int(8),
				VolumeType:          pulumi.String("gp3"),
				DeleteOnTermination: pulumi.Bool(true),
			},
			Tags: pulumi.StringMap{"Name": pulumi.String("pageleft")},
		})
		if err != nil {
			return err
		}

		// --- Elastic IP ---
		eip, err := ec2.NewEip(ctx, "pageleft-eip", &ec2.EipArgs{
			Instance: instance.ID(),
			Tags:     pulumi.StringMap{"Name": pulumi.String("pageleft")},
		})
		if err != nil {
			return err
		}

		// --- Route53 Hosted Zone ---
		zone, err := route53.NewZone(ctx, "pageleft-zone", &route53.ZoneArgs{
			Name: pulumi.String(domain),
		})
		if err != nil {
			return err
		}

		// --- DNS Records ---
		_, err = route53.NewRecord(ctx, "pageleft-a", &route53.RecordArgs{
			ZoneId:  zone.ZoneId,
			Name:    pulumi.String(domain),
			Type:    pulumi.String("A"),
			Ttl:     pulumi.Int(300),
			Records: pulumi.StringArray{eip.PublicIp},
		})
		if err != nil {
			return err
		}

		_, err = route53.NewRecord(ctx, "pageleft-www", &route53.RecordArgs{
			ZoneId:  zone.ZoneId,
			Name:    pulumi.String("www." + domain),
			Type:    pulumi.String("A"),
			Ttl:     pulumi.Int(300),
			Records: pulumi.StringArray{eip.PublicIp},
		})
		if err != nil {
			return err
		}

		// --- Outputs ---
		ctx.Export("publicIp", eip.PublicIp)
		ctx.Export("nameservers", zone.NameServers)
		ctx.Export("sshCommand", pulumi.Sprintf("ssh ubuntu@%s", eip.PublicIp))
		ctx.Export("siteUrl", pulumi.String("https://"+domain))

		return nil
	})
}
