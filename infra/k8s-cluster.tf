locals {
  zone_a_v4_cidr_blocks = "10.1.0.0/16"

  # четыре кластера для распараллеливания прогонов
  clusters = {
    k1 = { name = "obs-bench-k1", cluster_cidr = "10.112.0.0/16", service_cidr = "10.96.0.0/16" }
    k2 = { name = "obs-bench-k2", cluster_cidr = "10.113.0.0/16", service_cidr = "10.97.0.0/16" }
    k3 = { name = "obs-bench-k3", cluster_cidr = "10.114.0.0/16", service_cidr = "10.98.0.0/16" }
    k4 = { name = "obs-bench-k4", cluster_cidr = "10.115.0.0/16", service_cidr = "10.99.0.0/16" }
  }

  all_pod_service_cidrs = flatten([for k, v in local.clusters : [v.cluster_cidr, v.service_cidr]])
}

resource "yandex_vpc_network" "k8s-network" {
  description = "Network for the Managed Service for Kubernetes cluster"
  name        = "k8s-network"
}

resource "yandex_vpc_subnet" "subnet-a" {
  description    = "Subnet in ru-central1-a availability zone"
  name           = "subnet-a"
  zone           = var.zone
  network_id     = yandex_vpc_network.k8s-network.id
  v4_cidr_blocks = [local.zone_a_v4_cidr_blocks]
}

resource "yandex_vpc_security_group" "k8s-cluster-nodegroup-traffic" {
  description = "Service traffic between master and nodes."
  name        = "k8s-cluster-nodegroup-traffic"
  network_id  = yandex_vpc_network.k8s-network.id

  ingress {
    description       = "Health checks from network load balancer."
    from_port         = 0
    to_port           = 65535
    protocol          = "TCP"
    predefined_target = "loadbalancer_healthchecks"
  }
  ingress {
    description       = "Service traffic between master and nodes."
    from_port         = 0
    to_port           = 65535
    protocol          = "ANY"
    predefined_target = "self_security_group"
  }
  ingress {
    description    = "ICMP health checks from VPC subnets."
    protocol       = "ICMP"
    v4_cidr_blocks = [local.zone_a_v4_cidr_blocks]
  }
  egress {
    description       = "Outgoing service traffic between master and nodes."
    from_port         = 0
    to_port           = 65535
    protocol          = "ANY"
    predefined_target = "self_security_group"
  }
}

resource "yandex_vpc_security_group" "k8s-nodegroup-traffic" {
  description = "Traffic between pods and services."
  name        = "k8s-nodegroup-traffic"
  network_id  = yandex_vpc_network.k8s-network.id

  ingress {
    description    = "Traffic between pods and services (all clusters)."
    from_port      = 0
    to_port        = 65535
    protocol       = "ANY"
    v4_cidr_blocks = local.all_pod_service_cidrs
  }
  egress {
    description    = "Outgoing traffic to external resources."
    from_port      = 0
    to_port        = 65535
    protocol       = "ANY"
    v4_cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "yandex_vpc_security_group" "k8s-services-access" {
  name        = "k8s-services-access"
  description = "NodePort access from Internet."
  network_id  = yandex_vpc_network.k8s-network.id

  ingress {
    description    = "NodePort services."
    from_port      = 30000
    to_port        = 32767
    protocol       = "TCP"
    v4_cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "yandex_vpc_security_group" "k8s-ssh-access" {
  description = "SSH access to nodes."
  name        = "k8s-ssh-access"
  network_id  = yandex_vpc_network.k8s-network.id

  ingress {
    description    = "SSH."
    port           = 22
    protocol       = "TCP"
    v4_cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "yandex_vpc_security_group" "k8s-cluster-traffic" {
  description = "Kubernetes API access."
  name        = "k8s-cluster-traffic"
  network_id  = yandex_vpc_network.k8s-network.id

  ingress {
    description    = "Kubernetes API (443)."
    port           = 443
    protocol       = "TCP"
    v4_cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    description    = "Kubernetes API (6443)."
    port           = 6443
    protocol       = "TCP"
    v4_cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    description    = "Master to metric-server."
    port           = 4443
    protocol       = "TCP"
    v4_cidr_blocks = local.all_pod_service_cidrs
  }
}

resource "yandex_iam_service_account" "k8s-sa" {
  name = var.sa_name
}

resource "yandex_resourcemanager_folder_iam_binding" "k8s-clusters-agent" {
  folder_id = var.folder_id
  role      = "k8s.clusters.agent"
  members   = ["serviceAccount:${yandex_iam_service_account.k8s-sa.id}"]
}

resource "yandex_resourcemanager_folder_iam_binding" "k8s-tunnelClusters-agent" {
  folder_id = var.folder_id
  role      = "k8s.tunnelClusters.agent"
  members   = ["serviceAccount:${yandex_iam_service_account.k8s-sa.id}"]
}

resource "yandex_resourcemanager_folder_iam_binding" "vpc-publicAdmin" {
  folder_id = var.folder_id
  role      = "vpc.publicAdmin"
  members   = ["serviceAccount:${yandex_iam_service_account.k8s-sa.id}"]
}

resource "yandex_resourcemanager_folder_iam_binding" "images-puller" {
  folder_id = var.folder_id
  role      = "container-registry.images.puller"
  members   = ["serviceAccount:${yandex_iam_service_account.k8s-sa.id}"]
}

resource "yandex_resourcemanager_folder_iam_binding" "lb-admin" {
  folder_id = var.folder_id
  role      = "load-balancer.admin"
  members   = ["serviceAccount:${yandex_iam_service_account.k8s-sa.id}"]
}

resource "yandex_kubernetes_cluster" "k8s-cluster" {
  for_each = local.clusters

  description        = "obs-bench Kubernetes cluster ${each.key}"
  name               = each.value.name
  network_id         = yandex_vpc_network.k8s-network.id
  cluster_ipv4_range = each.value.cluster_cidr
  service_ipv4_range = each.value.service_cidr

  master {
    version = var.k8s_version

    master_location {
      zone      = yandex_vpc_subnet.subnet-a.zone
      subnet_id = yandex_vpc_subnet.subnet-a.id
    }

    public_ip = true

    security_group_ids = [
      yandex_vpc_security_group.k8s-cluster-nodegroup-traffic.id,
      yandex_vpc_security_group.k8s-cluster-traffic.id,
    ]
  }

  service_account_id      = yandex_iam_service_account.k8s-sa.id
  node_service_account_id = yandex_iam_service_account.k8s-sa.id

  depends_on = [
    yandex_resourcemanager_folder_iam_binding.k8s-clusters-agent,
    yandex_resourcemanager_folder_iam_binding.k8s-tunnelClusters-agent,
    yandex_resourcemanager_folder_iam_binding.vpc-publicAdmin,
    yandex_resourcemanager_folder_iam_binding.images-puller,
    yandex_resourcemanager_folder_iam_binding.lb-admin,
  ]
}

resource "yandex_kubernetes_node_group" "k8s-node-group" {
  for_each = local.clusters

  description = "Node group for obs-bench experiments (${each.key})"
  name        = "${each.value.name}-nodes"
  cluster_id  = yandex_kubernetes_cluster.k8s-cluster[each.key].id
  version     = var.k8s_version

  scale_policy {
    fixed_scale {
      size = var.node_count
    }
  }

  allocation_policy {
    location {
      zone = var.zone
    }
  }

  instance_template {
    platform_id = "standard-v3"

    network_interface {
      nat        = true
      subnet_ids = [yandex_vpc_subnet.subnet-a.id]
      security_group_ids = [
        yandex_vpc_security_group.k8s-cluster-nodegroup-traffic.id,
        yandex_vpc_security_group.k8s-nodegroup-traffic.id,
        yandex_vpc_security_group.k8s-services-access.id,
        yandex_vpc_security_group.k8s-ssh-access.id,
      ]
    }

    resources {
      cores  = var.node_cores
      memory = var.node_memory_gb
    }

    boot_disk {
      type = "network-ssd"
      size = var.node_disk_gb
    }
  }

  timeouts {
    create = "90m"
    update = "90m"
    delete = "60m"
  }
}
